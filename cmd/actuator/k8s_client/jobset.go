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
	"maps"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"xmanager_cloud/internal/logging"
	ee "xmanager_cloud/internal/toollibs/execution_environments"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/jobsetutil"
	"xmanager_cloud/validator/validator"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

const (
	kueueQueueLabel         = "kueue.x-k8s.io/queue-name"
	kueuePriorityClassLabel = "kueue.x-k8s.io/priority-class"
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

	jobMeta := buildJobMetadata(job, jobLabels, jobAnnotations)
	labels, annotations, podLabels, podAnnotations := jobMeta.labels, jobMeta.annotations, jobMeta.podLabels, jobMeta.podAnnotations

	var dependsOn []jobsetv1alpha2.DependsOn
	if depOrder := job.GetDependencyOrder(); depOrder.GetJobName() != "" {
		dependsOn = append(dependsOn, jobsetv1alpha2.DependsOn{
			Name:   depOrder.GetJobName(),
			Status: dependencyStatus(ctx, depOrder.GetJobDependencyState()),
		})
	}

	var activeDeadlineSeconds *int64
	if job.GetActiveDeadlineSeconds() > 0 {
		activeDeadlineSeconds = ptr.To(job.GetActiveDeadlineSeconds())
	}

	var terminationGracePeriodSeconds *int64
	if job.TerminationGracePeriodSeconds != nil && *job.TerminationGracePeriodSeconds >= 0 {
		terminationGracePeriodSeconds = ptr.To(*job.TerminationGracePeriodSeconds)
	}

	primaryName := jobsetutil.PrimaryContainerName(job)
	nodeSelector := copyNodeSelector(job.GetNodeSelector())

	hostNetwork, dnsPolicy := resolveDNSAndNetwork(c, job)

	restartPolicy := corev1.RestartPolicyNever
	if job.GetRestartPolicy() != "" {
		restartPolicy = corev1.RestartPolicy(job.GetRestartPolicy())
	}

	var pfp *batchv1.PodFailurePolicy
	if restartPolicy == corev1.RestartPolicyOnFailure {
		if retryLimit == 0 {
			retryLimit = constants.DefaultOnFailureRetryLimit
		}
	} else if !job.GetDisablePodFailurePolicy() {
		pfp = podFailurePolicy(primaryName)
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
				CompletionMode:          ptr.To(batchv1.IndexedCompletion),
				Completions:             ptr.To(int32(1)),
				PodFailurePolicy:        pfp,
				TTLSecondsAfterFinished: ptr.To(int32(defaultTTLSeconds)),
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

type jobMetadata struct {
	labels         map[string]string
	annotations    map[string]string
	podLabels      map[string]string
	podAnnotations map[string]string
}

func buildJobMetadata(job *esspb.KubernetesJob, jobLabels map[string]string, jobAnnotations map[string]string) jobMetadata {
	annotations := maps.Clone(jobAnnotations)
	if annotations == nil {
		annotations = make(map[string]string)
	}
	maps.Copy(annotations, job.GetAnnotations())

	labels := maps.Clone(jobLabels)
	if labels == nil {
		labels = make(map[string]string)
	}
	for k, v := range job.GetLabels() {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	podLabels := maps.Clone(labels)
	podAnnotations := maps.Clone(annotations)

	return jobMetadata{
		labels:         labels,
		annotations:    annotations,
		podLabels:      podLabels,
		podAnnotations: podAnnotations,
	}
}

type jobSetLabelsAndAnnotations struct {
	jobLabels          map[string]string
	jobSetLabels       map[string]string
	jobSetAnnotations  map[string]string
	defaultAnnotations map[string]string
}

func buildJobSetLabelsAndAnnotations(workUnit *esspb.WorkUnit, xid, wid, eid, author, namespace string, operationTime time.Time, priorities Priorities) jobSetLabelsAndAnnotations {
	jobLabels := map[string]string{
		kueueQueueLabel:             namespace,
		constants.ExperimentIDLabel: xid,
		constants.WorkUnitIDLabel:   wid,
		constants.ExecutionIDLabel:  eid,
	}
	maps.Copy(jobLabels, defaultLabels)

	jobSetLabels := map[string]string{
		kueueQueueLabel:         namespace,
		constants.LastEditLabel: strconv.FormatInt(operationTime.Unix(), 10),
	}
	maps.Copy(jobSetLabels, jobLabels)
	jobSetLabels[kueuePriorityClassLabel] = getPriorityClassName(priorities)

	defaultAnnotations := map[string]string{
		constants.AuthorAnnotation:                author,
		constants.AutoscalerSafeToEvictAnnotation: "false",
	}

	jobSetAnnotations := maps.Clone(defaultAnnotations)

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
	return jobSetLabelsAndAnnotations{
		jobLabels:          jobLabels,
		jobSetLabels:       jobSetLabels,
		jobSetAnnotations:  jobSetAnnotations,
		defaultAnnotations: defaultAnnotations,
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

	meta := buildJobSetLabelsAndAnnotations(workUnit, xid, wid, eid, author, namespace, operationTime, priorities)
	jobLabels, jobSetLabels, jobSetAnnotations, defaultAnnotations := meta.jobLabels, meta.jobSetLabels, meta.jobSetAnnotations, meta.defaultAnnotations

	// Inject the managed-xprof GCS destination whenever the cluster configures a
	// managed-xprof GCS bucket. This is intentionally decoupled from the ManagedXprof
	// artifact: the workload only needs to know where to write xprof output,
	// regardless of whether or when the artifact is attached.
	var xprofObjectPath string
	if bucket, bucketErr := c.ManagedXProfGCSBucket(ctx); bucketErr != nil {
		logging.Warn(ctx, "Failed to resolve managed-xprof GCS path via Actuator", "error", bucketErr)
	} else if bucket != "" {
		xprofObjectPath = fmt.Sprintf("%s/xmc-%s-%s-%s", strings.TrimSuffix(bucket, "/"), xid, wid, strconv.FormatInt(operationTime.Unix(), 10))
	}

	var replicatedJobs []jobsetv1alpha2.ReplicatedJob
	if workUnit.GetExecutable().GetJobset() != nil {
		for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
			rj := c.k8sJobToReplicatedJob(ctx, job, jobLabels, defaultAnnotations)
			injectManagedXprofGCSEnvVars(&rj, xprofObjectPath)
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
			Suspend:        ptr.To(false),
			SuccessPolicy:  buildJobSetSuccessPolicy(workUnit),
			FailurePolicy: &jobsetv1alpha2.FailurePolicy{
				// JobSet-level restarts are disabled (MaxRestarts: 0) to comply with the XMC
				// contract, which expects a work unit to execute at most once.
				MaxRestarts: 0,
				Rules:       []jobsetv1alpha2.FailurePolicyRule{},
			},
			TTLSecondsAfterFinished: ptr.To(int32(defaultTTLSeconds)),
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
	if err := c.evaluateComponentSnapshots(ctx, workUnit, namespace, jobSet); err != nil {
		return nil, err
	}
	if err := validateNonRestartingJobSetContract(jobSet); err != nil {
		return nil, err
	}
	return jobSet, nil
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
