// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/util/wait/wait"
	"golang/protobuf/v2/proto/proto"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/jobsetutil"
	"xmanager_cloud/logging/logging"
	ee "xmanager_cloud/toollibs/execution_environments/executionenvironments"
	"xmanager_cloud/validator/validator"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

const (
	kueueQueueLabel         = "kueue.x-k8s.io/queue-name"
	kueuePriorityClassLabel = "kueue.x-k8s.io/priority-class"
	kueueWorkloadAnnotation = "kueue.x-k8s.io/workload"
	// defaultTTLSeconds is the default time-to-live for JobSet and Jobs after finishing.
	defaultTTLSeconds = 7 * 24 * 60 * 60 // 7 days
)

var (
	// JobSetGVR is the GroupVersionResource for the JobSet custom resource.
	JobSetGVR = schema.GroupVersionResource{
		Group:    "jobset.x-k8s.io",
		Version:  "v1alpha2",
		Resource: "jobsets",
	}
	// KueueWorkloadGVR is the GroupVersionResource for the Kueue Workload custom resource.
	KueueWorkloadGVR = schema.GroupVersionResource{
		Group:    "kueue.x-k8s.io",
		Version:  "v1beta1",
		Resource: "workloads",
	}
	defaultLabels = map[string]string{
		constants.ManagedByLabel: constants.XmcLabelValue,
	}
)

func (c *Client) createJobSet(ctx context.Context, workUnit *esspb.WorkUnit, target, author string, operationTime time.Time, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	namespace := getNamespaceFromWorkUnit(workUnit)

	jobSet, err := c.constructJobSet(ctx, workUnit, target, author, namespace, operationTime, priorities)
	if err != nil {
		logging.Warn(ctx, "Failed to construct JobSet manifest", "error", err)
		return fmt.Errorf("failed to construct JobSet manifest: %w", err)
	}
	logging.Info(ctx, "Create JobSet", "jobSet", jobSet.GetName())

	ctx = logging.WithJobSetName(ctx, jobSet.GetName(), nil)

	// need to convert to unstructured.

	jobSetUnstructured, err := jobSetToUnstructured(jobSet)
	if err != nil {
		logging.Warn(ctx, "Failed to convert JobSet to unstructured", "error", err)
		return fmt.Errorf("failed to convert JobSet to unstructured: %w", err)
	}

	if err := c.preconditionsValidator.ValidateJobSetManifest(jobSetUnstructured); err != nil {
		logging.Warn(ctx, "Failed to validate JobSet manifest", "target", target, "error", err)
		return fmt.Errorf("failed to validate JobSet manifest for %q: %w", target, err)
	}

	name := jobSet.GetName()

	if err := c.ensureNamespaceExists(ctx, namespace); err != nil {
		logging.Error(ctx, "Failed to ensure namespace exists", "namespace", namespace, "error", err)
		return fmt.Errorf("failed to ensure namespace %q exists: %w", namespace, err)
	}

	_, err = c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Create(ctx, jobSetUnstructured, metav1.CreateOptions{})
	if err != nil {
		logging.Warn(ctx, "Failed to create JobSet", "error", err)
		return fmt.Errorf("failed to create JobSet %q in Kubernetes: %w", name, err)
	}

	logging.Info(ctx, "Created JobSet", "name", name, "namespace", namespace)

	return nil
}

func (c *Client) deleteJobSet(ctx context.Context, target, namespace string, operationTime time.Time) (bool, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	jobSetUnstructured, err := c.getJobSetByTargetAndNamespace(ctx, target, namespace)
	if err != nil {
		return false, err
	}
	if jobSetUnstructured == nil {
		logging.Warn(ctx, "JobSet not found for target, skipping deletion")
		return false, nil
	}

	jobSetName := jobSetUnstructured.GetName()
	ctx = logging.WithJobSetName(ctx, jobSetName, jobSetUnstructured.GetLabels())
	logging.Info(ctx, "Delete JobSet", "namespace", namespace, "jobSetName", jobSetName)

	valid, err := c.preconditionsValidator.IsActuationRequestValid(ctx, target, operationTime, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed to validate deleteJobSet request", "error", err)
		return false, err
	}
	if !valid {
		logging.Warn(ctx, "Discarding delete request")
		return false, nil
	}

	aligned, err := c.preconditionsValidator.IsExecutionIDAligned(ctx, target, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed during execution ID alignment check", "error", err, "target", target)
		return false, fmt.Errorf("failed during execution ID alignment check for %q: %w", target, err)
	}
	if !aligned {
		logging.Warn(ctx, "Jobset EID does not match target, skipping deletion")
		return false, nil
	}

	uid := jobSetUnstructured.GetUID()
	// Set the UID in the options, so we make sure we ONLY delete the Jobset for the current target
	// and not any other Jobset that may have been created in the meantime.
	deleteOptions := metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID: &uid,
		},
	}

	err = c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Delete(ctx, jobSetName, deleteOptions)
	if err != nil {
		logging.Warn(ctx, "Failed to delete JobSet", "namespace", namespace, "error", err)
		if apierrors.IsNotFound(err) {
			logging.Warn(ctx, "JobSet %s not found, assuming it was already deleted", jobSetName)
			return false, nil
		}
		return false, fmt.Errorf("failed to delete JobSet %q in namespace %q: %w", jobSetName, namespace, err)
	}

	logging.Debug(ctx, "Successfully initiated deletion of JobSet", "namespace", namespace)
	err = wait.PollImmediate(2*time.Second, 30*time.Second, func() (bool, error) {
		_, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err == wait.ErrWaitTimeout {
		logging.Warn(ctx, "Timed out waiting for JobSet to be deleted", "namespace", namespace, "jobSetName", jobSetName, "error", err)
		return false, fmt.Errorf("timed out waiting for jobset %q to be deleted: %w", jobSetName, err)
	}
	if err != nil {
		logging.Warn(ctx, "Failed while waiting for JobSet to be deleted", "namespace", namespace, "jobSetName", jobSetName, "error", err)
		return false, fmt.Errorf("failed while waiting for jobset %q to be deleted: %w", jobSetName, err)
	}

	logging.Info(ctx, "Deleted JobSet", "namespace", namespace, "jobSetName", jobSetName)

	return true, nil
}

func (c *Client) restartJobSet(ctx context.Context, workUnit *esspb.WorkUnit, target, author string, operationTime time.Time, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	logging.Info(ctx, "Restart JobSet", "target", target)
	namespace, err := c.getNamespaceFromWorkUnitWithFallback(ctx, workUnit, target)
	if err != nil {
		logging.Warn(ctx, "Failed to get namespace for target", "target", target, "error", err)
		return fmt.Errorf("failed to get namespace for target %q: %w", target, err)
	}

	jobSetUnstructured, err := c.getJobSetByTargetAndNamespace(ctx, target, namespace)
	if err == nil && jobSetUnstructured != nil {
		shouldCreate, err := c.validateAndDeleteJobSetForRestart(ctx, jobSetUnstructured, target, namespace, operationTime)
		if err != nil {
			return err
		}
		if !shouldCreate {
			return nil
		}
	}

	newTarget := workUnit.GetCurrentWorkUnitExecution().GetName()
	ensureQueueName(workUnit, namespace)

	if err := c.createJobSet(ctx, workUnit, newTarget, author, operationTime, priorities); err != nil {
		return fmt.Errorf("failed to create JobSet %q in Kubernetes during restart: %w", buildJobSetName(target), err)
	}
	logging.Info(ctx, "Restarted JobSet", "target", target)
	return nil
}

func (c *Client) validateAndDeleteJobSetForRestart(ctx context.Context, jobSetUnstructured *unstructured.Unstructured, target, namespace string, operationTime time.Time) (bool, error) {
	ctx = logging.WithJobSetName(ctx, jobSetUnstructured.GetName(), jobSetUnstructured.GetLabels())

	valid, err := c.preconditionsValidator.IsActuationRequestValid(ctx, target, operationTime, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed to validate restartJobSet request", "error", err)
		return false, err
	}
	if !valid {
		logging.Warn(ctx, "Discarding restart request")
		return false, nil
	}

	deleted, err := c.deleteJobSet(ctx, target, namespace, operationTime)
	if err != nil {
		logging.Warn(ctx, "Failed to delete JobSet during restart", "error", err)
	}

	if !deleted {
		logging.Warn(ctx, "JobSet not deleted during restart, proceeding with creation")
	}
	return true, nil
}

func (c *Client) updateJobSetPriority(ctx context.Context, target string, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	logging.Info(ctx, "Update JobSet priority", "target", target, "priorities", priorities)
	jobSetName := buildJobSetName(target)
	if jobSetName == "" {
		return fmt.Errorf("failed to generate jobset name for target %q", target)
	}

	ctx = logging.WithJobSetName(ctx, jobSetName, nil)

	namespace, err := c.getNamespaceFromTarget(ctx, target)
	if err != nil {
		return fmt.Errorf("failed to get namespace by target %q: %w", target, err)
	}

	priorityClassName := getPriorityClassName(priorities)
	patchPayload := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				kueuePriorityClassLabel: priorityClassName,
			},
		},
	}
	patchBytes, err := json.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal patch payload for jobset %q: %w", jobSetName, err)
	}

	_, err = c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Patch(ctx, jobSetName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		logging.Warn(ctx, "Failed to patch JobSet", "namespace", namespace, "error", err)
		return fmt.Errorf("failed to patch JobSet %q in namespace %q: %w", jobSetName, namespace, err)
	}
	logging.Info(ctx, "Updated JobSet priority", "namespace", namespace, "priorityClassName", priorityClassName)

	return nil
}

func jobSetToUnstructured(jobSet *jobsetv1alpha2.JobSet) (*unstructured.Unstructured, error) {
	jobSetObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(jobSet)
	if err != nil {
		return nil, fmt.Errorf("failed to convert JobSet to unstructured: %w", err)
	}
	jobSetUnstructured := &unstructured.Unstructured{Object: jobSetObj}

	return jobSetUnstructured, nil
}

func (c *Client) k8sJobToReplicatedJob(ctx context.Context, job *esspb.KubernetesJob, jobLabels map[string]string, jobAnnotations map[string]string) jobsetv1alpha2.ReplicatedJob {
	retryLimit := job.GetRetryLimit()

	// Merge job annotations with default annotations.
	annotations := make(map[string]string)
	for k, v := range jobAnnotations {
		annotations[k] = v
	}
	for k, v := range job.GetAnnotations() {
		annotations[k] = v
	}

	// Merge custom job labels with base job labels, preventing overrides of system labels.
	labels := make(map[string]string, len(jobLabels)+len(job.GetLabels()))
	for k, v := range jobLabels {
		labels[k] = v
	}
	for k, v := range job.GetLabels() {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	// Create a shallow copy for PodTemplateSpec to prevent sharing the map pointer.
	podLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		podLabels[k] = v
	}

	// Create a shallow copy for PodTemplateSpec to prevent sharing the map pointer.
	podAnnotations := make(map[string]string, len(annotations))
	for k, v := range annotations {
		podAnnotations[k] = v
	}

	var dependsOn []jobsetv1alpha2.DependsOn
	if depOrder := job.GetDependencyOrder(); depOrder.GetJobName() != "" {
		dependsOn = append(dependsOn, jobsetv1alpha2.DependsOn{
			Name:   depOrder.GetJobName(),
			Status: dependencyStatus(ctx, depOrder.GetJobDependencyState()),
		})
	}

	var activeDeadlineSeconds *int64
	if job.GetActiveDeadlineSeconds() > 0 {
		activeDeadlineSeconds = proto.Int64(job.GetActiveDeadlineSeconds())
	}

	var terminationGracePeriodSeconds *int64
	if job.TerminationGracePeriodSeconds != nil && *job.TerminationGracePeriodSeconds >= 0 {
		terminationGracePeriodSeconds = proto.Int64(*job.TerminationGracePeriodSeconds)
	}

	primaryName := jobsetutil.PrimaryContainerName(job)
	nodeSelector := copyNodeSelector(job.GetNodeSelector())

	var hostNetwork bool
	var dnsPolicy corev1.DNSPolicy
	if c != nil && c.RuntimeConfig.AllowHostNetwork && job.GetHostNetwork() {
		hostNetwork = true
		if job.GetDnsPolicy() != "" {
			dnsPolicy = corev1.DNSPolicy(job.GetDnsPolicy())
		} else {
			dnsPolicy = corev1.DNSClusterFirstWithHostNet
		}
	} else if job.GetDnsPolicy() != "" {
		dnsPolicy = corev1.DNSPolicy(job.GetDnsPolicy())
	}

	restartPolicy := corev1.RestartPolicyNever
	if job.GetRestartPolicy() != "" {
		restartPolicy = corev1.RestartPolicy(job.GetRestartPolicy())
	}

	return jobsetv1alpha2.ReplicatedJob{
		Name:      job.GetName(),
		GroupName: job.GetGroupName(),
		Replicas:  job.GetReplicas(),
		DependsOn: dependsOn,
		Template: batchv1.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit:            &retryLimit,
				CompletionMode:          completionModePtr(batchv1.IndexedCompletion),
				Completions:             proto.Int32(1),
				PodFailurePolicy:        podFailurePolicy(primaryName),
				TTLSecondsAfterFinished: proto.Int32(defaultTTLSeconds),
				ActiveDeadlineSeconds:   activeDeadlineSeconds,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      podLabels,
						Annotations: podAnnotations,
					},
					Spec: corev1.PodSpec{
						ServiceAccountName:            job.GetServiceAccount(),
						PriorityClassName:             job.GetPriorityClassName(),
						HostNetwork:                   hostNetwork,
						DNSPolicy:                     dnsPolicy,
						InitContainers:                c.buildInitContainersForJob(job),
						Containers:                    c.buildContainersForJob(job),
						RestartPolicy:                 restartPolicy,
						SecurityContext:               defaultPodSecurityContext(),
						Volumes:                       generateVolumesForJob(job),
						NodeSelector:                  nodeSelector,
						TerminationGracePeriodSeconds: terminationGracePeriodSeconds,
					},
				},
			},
		},
	}
}

// injectManagedXprofGCSEnvVars appends the managed-xprof GCS destination env vars to
// every container of the replicated job so all workloads in the pod know where to
// write xprof output, not just the primary container. No-op when no bucket was
// resolved.
func injectManagedXprofGCSEnvVars(rj *jobsetv1alpha2.ReplicatedJob, xprofBucketURL, xprofObjectPath string) {
	if xprofBucketURL == "" && xprofObjectPath == "" {
		return
	}
	injectManagedXprofGCSEnvVarsToContainers(rj.Template.Spec.Template.Spec.Containers, xprofBucketURL, xprofObjectPath)
	injectManagedXprofGCSEnvVarsToContainers(rj.Template.Spec.Template.Spec.InitContainers, xprofBucketURL, xprofObjectPath)
}

// injectManagedXprofGCSEnvVarsToContainers appends the managed-xprof GCS destination
// env vars to each container in the provided slice in place.
func injectManagedXprofGCSEnvVarsToContainers(containers []corev1.Container, xprofBucketURL, xprofObjectPath string) {
	for i := range containers {
		// Index into the slice so appends write back to the JobSet in place.
		container := &containers[i]
		if xprofBucketURL != "" {
			container.Env = append(container.Env, corev1.EnvVar{Name: constants.XmcEnvVarManagedXprofGCSBucket, Value: xprofBucketURL})
		}
		if xprofObjectPath != "" {
			container.Env = append(container.Env, corev1.EnvVar{Name: constants.XmcEnvVarManagedXprofGCSObjectFolder, Value: xprofObjectPath})
		}
	}
}

// copyNodeSelector creates an immutable copy of the job node selector map.
func copyNodeSelector(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copyMap := make(map[string]string, len(source))
	for k, v := range source {
		copyMap[k] = v
	}
	return copyMap
}

func (c *Client) buildInitContainersForJob(job *esspb.KubernetesJob) []corev1.Container {
	total := len(job.GetInitContainers()) + len(job.GetSidecars())
	if total == 0 {
		return nil
	}
	initContainers := make([]corev1.Container, 0, total)
	for i, initContainer := range job.GetInitContainers() {
		name := jobsetutil.InitContainerName(job, initContainer, i)
		container := c.buildContainer(name, initContainer)
		if strings.EqualFold(initContainer.GetRestartPolicy(), "Always") {
			restartPolicyAlways := corev1.ContainerRestartPolicyAlways
			container.RestartPolicy = &restartPolicyAlways
		}
		initContainers = append(initContainers, container)
	}
	restartPolicyAlways := corev1.ContainerRestartPolicyAlways
	for i, sidecar := range job.GetSidecars() {
		name := jobsetutil.SidecarContainerName(job, sidecar, i)
		container := c.buildContainer(name, sidecar)
		container.RestartPolicy = &restartPolicyAlways
		initContainers = append(initContainers, container)
	}
	return initContainers
}

func (c *Client) buildContainersForJob(job *esspb.KubernetesJob) []corev1.Container {
	primaryName := jobsetutil.PrimaryContainerName(job)
	return []corev1.Container{
		c.buildContainer(primaryName, job.GetSpec()),
	}
}

func buildContainerPorts(spec *esspb.ExecutableSpec) []corev1.ContainerPort {
	if len(spec.GetPorts()) == 0 {
		return nil
	}
	ports := make([]corev1.ContainerPort, 0, len(spec.GetPorts()))
	for _, p := range spec.GetPorts() {
		var protocol corev1.Protocol
		switch p.GetProtocol() {
		case esspb.ContainerPort_UDP:
			protocol = corev1.ProtocolUDP
		case esspb.ContainerPort_SCTP:
			protocol = corev1.ProtocolSCTP
		default:
			protocol = corev1.ProtocolTCP
		}
		ports = append(ports, corev1.ContainerPort{
			Name:          p.GetName(),
			ContainerPort: p.GetContainerPort(),
			Protocol:      protocol,
			HostPort:      p.GetHostPort(),
		})
	}
	return ports
}

func (c *Client) buildContainer(containerName string, spec *esspb.ExecutableSpec) corev1.Container {
	envVars := c.generateEnvVarsForSpec(spec)
	ports := buildContainerPorts(spec)
	var args []string
	specArgs := spec.GetArgs()
	if len(specArgs) > 0 {
		keys := make([]string, 0, len(specArgs))
		for k := range specArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, fmt.Sprintf("--%s=%s", k, specArgs[k]))
		}
	}
	args = append(args, spec.GetArgsList()...)
	sc := defaultSecurityContext()
	if c != nil && c.RuntimeConfig.AllowPrivilegedContainers && spec.GetPrivileged() {
		sc.Privileged = proto.Bool(true)
		sc.AllowPrivilegeEscalation = proto.Bool(true)
	}

	return corev1.Container{
		Name:                     containerName,
		Image:                    spec.GetDockerImage(),
		ImagePullPolicy:          corev1.PullAlways,
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		Command:                  spec.GetCommand(),
		Args:                     args,
		Env:                      envVars,
		Ports:                    ports,
		SecurityContext:          sc,
		VolumeMounts:             generateVolumeMountsForSpec(spec),
	}
}

// constructJobSet builds the complete JobSet manifest as a Go map suitable for the dynamic client.
func (c *Client) constructJobSet(ctx context.Context, workUnit *esspb.WorkUnit, target string, author string, namespace string, operationTime time.Time, priorities Priorities) (*jobsetv1alpha2.JobSet, error) {
	xidInt, widInt, eidInt, err := validator.ValidateWorkUnitExecutionName(target)
	if err != nil {
		return nil, err
	}
	xid := strconv.FormatInt(xidInt, 10)
	wid := strconv.FormatInt(widInt, 10)
	eid := strconv.FormatInt(eidInt, 10)

	jobLabels := map[string]string{
		kueueQueueLabel:             namespace,
		constants.ExperimentIDLabel: xid,
		constants.WorkUnitIDLabel:   wid,
		constants.ExecutionIDLabel:  eid,
	}
	for k, v := range defaultLabels {
		jobLabels[k] = v
	}

	jobSetLabels := map[string]string{
		kueueQueueLabel:         namespace,
		constants.LastEditLabel: strconv.FormatInt(operationTime.Unix(), 10),
	}
	for k, v := range jobLabels {
		jobSetLabels[k] = v
	}
	jobSetLabels[kueuePriorityClassLabel] = getPriorityClassName(priorities)

	defaultAnnotations := map[string]string{
		constants.AuthorAnnotation:                author,
		constants.AutoscalerSafeToEvictAnnotation: "false",
	}

	jobSetAnnotations := make(map[string]string, len(defaultAnnotations))
	for k, v := range defaultAnnotations {
		jobSetAnnotations[k] = v
	}

	if jobset := workUnit.GetExecutable().GetJobset(); jobset != nil {
		for k, v := range jobset.GetAnnotations() {
			if _, exists := jobSetAnnotations[k]; !exists {
				jobSetAnnotations[k] = v
			}
		}
		for k, v := range jobset.GetLabels() {
			if _, exists := jobSetLabels[k]; !exists {
				jobSetLabels[k] = v
			}
		}
	}

	// Inject the managed-xprof GCS destination whenever the cluster configures a
	// managed-xprof GCS bucket. This is intentionally decoupled from the ManagedXprof
	// artifact: the workload only needs to know where to write xprof output,
	// regardless of whether or when the artifact is attached.
	var xprofBucketURL string
	var xprofObjectPath string
	if bucket, bucketErr := c.ManagedXProfGCSBucket(ctx); bucketErr != nil {
		logging.Warn(ctx, "Failed to resolve managed-xprof GCS path via Actuator", "error", bucketErr)
	} else if bucket != "" {
		xprofBucketURL = bucket
		xprofObjectPath = fmt.Sprintf("%s/xmc-%s-%s-%s", strings.TrimSuffix(bucket, "/"), xid, wid, strconv.FormatInt(operationTime.Unix(), 10))
	}

	var replicatedJobs []jobsetv1alpha2.ReplicatedJob
	if workUnit.GetExecutable().GetJobset() != nil {
		for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
			rj := c.k8sJobToReplicatedJob(ctx, job, jobLabels, defaultAnnotations)
			injectManagedXprofGCSEnvVars(&rj, xprofBucketURL, xprofObjectPath)
			replicatedJobs = append(replicatedJobs, rj)
		}
	}

	jobSetName := buildJobSetName(target)
	jobSet := &jobsetv1alpha2.JobSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: jobsetv1alpha2.GroupVersion.String(),
			Kind:       "JobSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobSetName,
			Namespace:   namespace,
			Labels:      jobSetLabels,
			Annotations: jobSetAnnotations,
			Finalizers:  []string{constants.JobSetFinalizer},
		},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: replicatedJobs,
			Network:        buildJobSetNetwork(workUnit, jobSetName),
			Coordinator:    buildJobSetCoordinator(workUnit),
			Suspend:        boolPtr(false),
			SuccessPolicy:  buildJobSetSuccessPolicy(workUnit),
			FailurePolicy: &jobsetv1alpha2.FailurePolicy{
				// JobSet-level restarts are disabled (MaxRestarts: 0) to comply with the XMC
				// contract, which expects a work unit to execute at most once.
				MaxRestarts: 0,
				Rules:       []jobsetv1alpha2.FailurePolicyRule{},
			},
			TTLSecondsAfterFinished: proto.Int32(defaultTTLSeconds),
		},
	}

	executionEnv, ok := ee.Lookup(c.RuntimeConfig.DefaultExecutionEnvironment)
	if !ok {
		logging.Error(ctx, "Failed to get execution environment", "execution_environment", c.RuntimeConfig.DefaultExecutionEnvironment)
		return nil, fmt.Errorf("failed to get execution environment: %q", c.RuntimeConfig.DefaultExecutionEnvironment)
	}
	jobSet, err = executionEnv.InjectOnJobSetCreation(ctx, workUnit, jobSet)
	if err != nil {
		return nil, err
	}
	ensureCoordinatorIndicesValid(jobSet)
	if err := validateNonRestartingJobSetContract(jobSet); err != nil {
		return nil, err
	}
	return jobSet, nil
}

// validateNonRestartingJobSetContract validates that the JobSet complies with the XMC Non-Restarting JobSets contract.
func validateNonRestartingJobSetContract(jobSet *jobsetv1alpha2.JobSet) error {
	if jobSet == nil {
		return nil
	}
	if jobSet.Spec.FailurePolicy != nil && jobSet.Spec.FailurePolicy.MaxRestarts != 0 {
		return fmt.Errorf("contract violation: JobSet MaxRestarts must be 0, got %d", jobSet.Spec.FailurePolicy.MaxRestarts)
	}
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		rp := rj.Template.Spec.Template.Spec.RestartPolicy
		if rp != corev1.RestartPolicyNever && rp != corev1.RestartPolicyOnFailure {
			return fmt.Errorf("contract violation: replicated job %q pod restartPolicy must be Never or OnFailure, got %s", rj.Name, rp)
		}
	}
	return nil
}

// ensureCoordinatorIndicesValid adjusts Coordinator indices if the execution environment collapsed a multi-replica job to a multi-pod job.
func ensureCoordinatorIndicesValid(jobSet *jobsetv1alpha2.JobSet) {
	if jobSet == nil || jobSet.Spec.Coordinator == nil {
		return
	}
	coord := jobSet.Spec.Coordinator
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		if rj.Name == coord.ReplicatedJob && rj.Replicas == 1 && coord.JobIndex > 0 {
			coord.PodIndex = coord.JobIndex + coord.PodIndex
			coord.JobIndex = 0
			return
		}
	}
}

func ensureQueueName(workUnit *esspb.WorkUnit, namespace string) {
	if workUnit.GetExecutable() == nil {
		workUnit.Executable = &esspb.Executable{}
	}
	if workUnit.GetExecutable().GetSchedulingConstraints() == nil {
		workUnit.GetExecutable().SchedulingConstraints = &esspb.SchedulingConstraints{}
	}
	if workUnit.GetExecutable().GetSchedulingConstraints().GetQueueName() == "" {
		workUnit.GetExecutable().GetSchedulingConstraints().QueueName = namespace
	}
}

func buildJobSetName(target string) string {
	ctx := context.Background()
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	xid, wid, _, err := validator.ValidateWorkUnitExecutionName(target)
	if err != nil {
		logging.Warn(ctx, "Failed to extract IDs from target", "target", target, "error", err)
		return ""
	}
	return fmt.Sprintf("xmc-operation-%d-%d", xid, wid)
}

func getNamespaceFromWorkUnit(workUnit *esspb.WorkUnit) string {
	return workUnit.GetExecutable().GetSchedulingConstraints().GetQueueName()
}

func getPriorityClassName(priorities Priorities) string {
	expLevel := priorityToString(priorities.ExperimentPriority)
	wuLevel := priorityToString(priorities.WorkUnitPriority)

	// This matches the priority class names in
	// /workload_priority_classes/*.yaml
	return fmt.Sprintf("exp-%s-wu-%s", expLevel, wuLevel)
}

func priorityToString(priority esspb.Priority) string {
	switch priority {
	case esspb.Priority_HIGH:
		return "high"
	case esspb.Priority_NORMAL:
		return "medium"
	case esspb.Priority_LOW:
		return "low"
	default:
		return "low"
	}
}

// shouldEnableJobSetNetwork determines if stable DNS hostnames should be enabled.
func shouldEnableJobSetNetwork(workUnit *esspb.WorkUnit) bool {
	exec := workUnit.GetExecutable()
	if exec == nil {
		return false
	}
	jobset := exec.GetJobset()
	if jobset == nil {
		return false
	}

	if net := jobset.GetNetwork(); net != nil && net.EnableDnsHostnames != nil {
		return *net.EnableDnsHostnames
	}

	jobs := jobset.GetJobs()
	if len(jobs) > 1 {
		return true
	}
	for _, job := range jobs {
		if job.GetReplicas() > 1 {
			return true
		}
	}
	return false
}

// buildJobSetNetwork builds the JobSet Network specification or returns nil.
func buildJobSetNetwork(workUnit *esspb.WorkUnit, jobSetName string) *jobsetv1alpha2.Network {
	if !shouldEnableJobSetNetwork(workUnit) {
		return nil
	}

	net := workUnit.GetExecutable().GetJobset().GetNetwork()
	subdomain := jobSetName
	if net != nil && net.GetSubdomain() != "" {
		subdomain = net.GetSubdomain()
	}

	publishNotReady := true
	if net != nil && net.PublishNotReadyAddresses != nil {
		publishNotReady = *net.PublishNotReadyAddresses
	}

	return &jobsetv1alpha2.Network{
		EnableDNSHostnames:       boolPtr(true),
		Subdomain:                subdomain,
		PublishNotReadyAddresses: boolPtr(publishNotReady),
	}
}

func buildJobSetSuccessPolicy(workUnit *esspb.WorkUnit) *jobsetv1alpha2.SuccessPolicy {
	exec := workUnit.GetExecutable()
	if exec == nil || exec.GetJobset() == nil {
		return &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}
	}
	policy := exec.GetJobset().GetSuccessPolicy()
	if policy != nil {
		return translateSuccessPolicy(policy)
	}
	if hasJobNamed(exec.GetJobset().GetJobs(), constants.PathwaysHeadJobName) {
		return &jobsetv1alpha2.SuccessPolicy{
			Operator:             jobsetv1alpha2.OperatorAll,
			TargetReplicatedJobs: []string{constants.PathwaysHeadJobName},
		}
	}
	return &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}
}

func translateSuccessPolicy(policy *esspb.JobSetExecutable_SuccessPolicy) *jobsetv1alpha2.SuccessPolicy {
	operator := jobsetv1alpha2.OperatorAll
	if policy.GetOperator() == esspb.JobSetExecutable_SuccessPolicy_ANY {
		operator = jobsetv1alpha2.OperatorAny
	}
	return &jobsetv1alpha2.SuccessPolicy{
		Operator:             operator,
		TargetReplicatedJobs: policy.GetTargetReplicatedJobs(),
	}
}

func hasJobNamed(jobs []*esspb.KubernetesJob, name string) bool {
	for _, job := range jobs {
		if job.GetName() == name {
			return true
		}
	}
	return false
}

// buildJobSetCoordinator builds the JobSet Coordinator specification or returns nil.
func buildJobSetCoordinator(workUnit *esspb.WorkUnit) *jobsetv1alpha2.Coordinator {
	if !shouldEnableJobSetCoordinator(workUnit) {
		return nil
	}

	coord := workUnit.GetExecutable().GetJobset().GetCoordinator()
	return &jobsetv1alpha2.Coordinator{
		ReplicatedJob: coord.GetReplicatedJob(),
		JobIndex:      int(coord.GetJobIndex()),
		PodIndex:      int(coord.GetPodIndex()),
	}
}

// shouldEnableJobSetCoordinator returns true if the WorkUnit specifies a valid coordinator.
func shouldEnableJobSetCoordinator(workUnit *esspb.WorkUnit) bool {
	if workUnit == nil || workUnit.GetExecutable() == nil || workUnit.GetExecutable().GetJobset() == nil {
		return false
	}
	coord := workUnit.GetExecutable().GetJobset().GetCoordinator()
	return coord != nil && coord.GetReplicatedJob() != ""
}

func boolPtr(b bool) *bool {
	return &b
}

func completionModePtr(cm batchv1.CompletionMode) *batchv1.CompletionMode {
	return &cm
}

func parseEnvVarSource(src *esspb.EnvVarSource) *corev1.EnvVarSource {
	if src == nil {
		return nil
	}
	switch ref := src.GetReference().(type) {
	case *esspb.EnvVarSource_FieldRef:
		if ref.FieldRef == nil {
			return nil
		}
		return &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath:  ref.FieldRef.FieldPath,
				APIVersion: ref.FieldRef.ApiVersion,
			},
		}
	case *esspb.EnvVarSource_SecretKeyRef:
		if ref.SecretKeyRef == nil {
			return nil
		}
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ref.SecretKeyRef.Name,
				},
				Key:      ref.SecretKeyRef.Key,
				Optional: ref.SecretKeyRef.Optional,
			},
		}
	default:
		return nil
	}
}

func (c *Client) generateEnvVars(job *esspb.KubernetesJob) []corev1.EnvVar {
	return c.generateEnvVarsForSpec(job.GetSpec())
}

func (c *Client) generateEnvVarsForSpec(spec *esspb.ExecutableSpec) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for k, v := range spec.GetEnvVars() {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	for _, ev := range spec.GetEnv() {
		if ev == nil || ev.Name == "" {
			continue
		}
		k8sEnvVar := corev1.EnvVar{Name: ev.Name}
		switch src := ev.GetSource().(type) {
		case *esspb.EnvVar_Value:
			k8sEnvVar.Value = src.Value
			envVars = append(envVars, k8sEnvVar)
		case *esspb.EnvVar_ValueFrom:
			if k8sSource := parseEnvVarSource(src.ValueFrom); k8sSource != nil {
				k8sEnvVar.ValueFrom = k8sSource
				envVars = append(envVars, k8sEnvVar)
			}
		}
	}
	metricsEnabled := false
	if c != nil {
		metricsEnabled = c.RuntimeConfig.MetricsEnabled
	}
	envVars = append(envVars, corev1.EnvVar{Name: constants.XmcEnvVarManagedBy, Value: "true"})
	envVars = append(envVars, corev1.EnvVar{Name: constants.XmcEnvVarMetricsEnabled, Value: strconv.FormatBool(metricsEnabled)})

	envVarFieldRefs := []struct {
		name      string
		fieldPath string
	}{
		{constants.XmcEnvVarNamespace, "metadata.namespace"},
		{constants.XmcEnvVarQueueName, fmt.Sprintf("metadata.labels['%s']", kueueQueueLabel)},
		{constants.XmcEnvVarExperimentID, fmt.Sprintf("metadata.labels['%s']", constants.ExperimentIDLabel)},
		{constants.XmcEnvVarWorkUnitID, fmt.Sprintf("metadata.labels['%s']", constants.WorkUnitIDLabel)},
		{constants.XmcEnvVarExecutionID, fmt.Sprintf("metadata.labels['%s']", constants.ExecutionIDLabel)},
		{constants.XmcEnvVarAuthor, fmt.Sprintf("metadata.annotations['%s']", constants.AuthorAnnotation)},
		{constants.XmcEnvVarJobIndex, fmt.Sprintf("metadata.labels['%s']", constants.JobSetJobIndexLabel)},
		{constants.XmcEnvVarJobName, fmt.Sprintf("metadata.labels['%s']", constants.JobSetReplicatedJobNameLabel)},
	}

	for _, ev := range envVarFieldRefs {
		envVars = append(envVars, corev1.EnvVar{
			Name: ev.name,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: ev.fieldPath,
				},
			},
		})
	}

	return envVars
}

// defaultSecurityContext returns the default security context for XManager Cloud jobs.
// To the inevitable engineer tinkering with this, please read the security documentation for K8s
// thoroughly before changing this. Unless you're absolutely sure what you're doing, please don't change this.
func defaultSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: proto.Bool(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{
				"ALL",
			},
		},
		Privileged: proto.Bool(false),
	}
}

// defaultPodSecurityContext returns the default pod security context for XManager Cloud jobs.
// To the inevitable engineer tinkering with this, please read the security documentation for K8s
// thoroughly before changing this. Unless you're absolutely sure what you're doing, please don't change this.
func defaultPodSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsGroup: proto.Int64(1000),
		RunAsUser:  proto.Int64(1000),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// podFailurePolicy returns a PodFailurePolicy that allows users to:
// 1. Throw error code 50 for early job exit.
// 2. Throw error code 42 for early pod exit that doesn't count against the backoff limit.
// 3. Ignore pod disruptions for the purposes of the backoff limit.
func podFailurePolicy(containerName string) *batchv1.PodFailurePolicy {
	return &batchv1.PodFailurePolicy{
		Rules: []batchv1.PodFailurePolicyRule{
			// Do not count pod disruptions as failures.
			{
				Action: batchv1.PodFailurePolicyActionIgnore,
				OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
					{
						Type: "DisruptionTarget",
					},
				},
			},

			// Allow users to throw an error code for early exit.
			{
				Action: batchv1.PodFailurePolicyActionFailJob,
				OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
					ContainerName: proto.String(containerName),
					Operator:      "In",
					Values:        []int32{50},
				},
			},

			// Allow users to throw an error code that doesn't count against the backoff limit.
			{
				Action: batchv1.PodFailurePolicyActionIgnore,
				OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
					ContainerName: proto.String(containerName),
					Operator:      "In",
					Values:        []int32{42},
				},
			},
		},
	}
}

func dependencyStatus(ctx context.Context, state esspb.KubernetesJob_DependencyOrder_JobDependencyState) jobsetv1alpha2.DependsOnStatus {
	switch state {
	case esspb.KubernetesJob_DependencyOrder_READY:
		return jobsetv1alpha2.DependencyReady
	case esspb.KubernetesJob_DependencyOrder_COMPLETED:
		return jobsetv1alpha2.DependencyComplete
	default:
		logging.Warn(ctx, "Unknown dependency status, defaulting to Ready", "status", state)
		return jobsetv1alpha2.DependencyReady
	}
}
