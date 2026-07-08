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
	"strconv"
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
	logging.Info(ctx, "Create JobSet", "jobSet", jobSet.GetName())
	if err != nil {
		logging.Warn(ctx, "Failed to construct JobSet manifest", "error", err)
		return fmt.Errorf("failed to construct JobSet manifest: %w", err)
	}

	ctx = logging.WithJobSetName(ctx, jobSet.GetName(), nil)

	// need to convert to unstructured.

	jobSetUnstructured, err := jobSetToUnstructured(jobSet)
	if err != nil {
		logging.Warn(ctx, "Failed to convert JobSet to unstructured", "error", err)
		return fmt.Errorf("failed to convert JobSet to unstructured: %w", err)
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

	targetMatches, uid, err := c.getJobsetUIDByTarget(ctx, target, namespace)
	if err != nil {
		return false, fmt.Errorf("getJobsetUIDByTarget failed in deleteJobSet: %w", err)
	}
	if !targetMatches {
		logging.Warn(ctx, "Jobset EID does not match target, skipping deletion")
		return false, nil
	}

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
		ctx = logging.WithJobSetName(ctx, jobSetUnstructured.GetName(), jobSetUnstructured.GetLabels())

		valid, err := c.preconditionsValidator.IsActuationRequestValid(ctx, target, operationTime, jobSetUnstructured)
		if err != nil {
			logging.Warn(ctx, "Failed to validate restartJobSet request", "error", err)
			return err
		}
		if !valid {
			logging.Warn(ctx, "Discarding restart request")
			return nil
		}

		deleted, err := c.deleteJobSet(ctx, target, namespace, operationTime)
		if err != nil {
			logging.Warn(ctx, "Failed to delete JobSet during restart", "error", err)
		}

		if !deleted {
			logging.Warn(ctx, "JobSet not deleted during restart, proceeding with creation")
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

func k8sJobToReplicatedJob(ctx context.Context, job *esspb.KubernetesJob, jobLabels map[string]string, jobAnnotations map[string]string) jobsetv1alpha2.ReplicatedJob {
	retryLimit := job.GetRetryLimit()

	// Merge job annotations with default annotations.
	annotations := make(map[string]string)
	for k, v := range jobAnnotations {
		annotations[k] = v
	}
	for k, v := range job.GetAnnotations() {
		annotations[k] = v
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

	primaryName := jobsetutil.PrimaryContainerName(job)

	return jobsetv1alpha2.ReplicatedJob{
		Name:      job.GetName(),
		Replicas:  job.GetReplicas(),
		DependsOn: dependsOn,
		Template: batchv1.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      jobLabels,
				Annotations: annotations,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit:            &retryLimit,
				PodFailurePolicy:        podFailurePolicy(primaryName),
				TTLSecondsAfterFinished: proto.Int32(defaultTTLSeconds),
				ActiveDeadlineSeconds:   activeDeadlineSeconds,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      jobLabels,
						Annotations: annotations,
					},
					Spec: corev1.PodSpec{
						ServiceAccountName: job.GetServiceAccount(),
						Containers:         buildContainersForJob(job),
						RestartPolicy:      corev1.RestartPolicyNever,
						SecurityContext:    defaultPodSecurityContext(),
						Volumes:            defaultVolumes(),
					},
				},
			},
		},
	}
}

func buildContainersForJob(job *esspb.KubernetesJob) []corev1.Container {
	primaryName := jobsetutil.PrimaryContainerName(job)
	containers := []corev1.Container{
		buildContainer(primaryName, job.GetSpec()),
	}
	for i, sidecar := range job.GetSidecars() {
		sidecarName := jobsetutil.SidecarContainerName(job, sidecar, i)
		containers = append(containers, buildContainer(sidecarName, sidecar))
	}
	return containers
}

func buildContainer(containerName string, spec *esspb.ExecutableSpec) corev1.Container {
	envVars := generateEnvVarsForSpec(spec)
	var args []string
	for k, v := range spec.GetArgs() {
		args = append(args, fmt.Sprintf("--%s=%s", k, v))
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
		SecurityContext:          defaultSecurityContext(),
		VolumeMounts:             defaultVolumeMounts(),
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

	jobSetAnnotations := map[string]string{
		constants.AuthorAnnotation:                author,
		constants.AutoscalerSafeToEvictAnnotation: "false",
	}

	var replicatedJobs []jobsetv1alpha2.ReplicatedJob
	if workUnit.GetExecutable().GetJobset() != nil {
		for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
			replicatedJobs = append(replicatedJobs, k8sJobToReplicatedJob(ctx, job, jobLabels, jobSetAnnotations))
		}
	}

	jobSet := &jobsetv1alpha2.JobSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: jobsetv1alpha2.GroupVersion.String(),
			Kind:       "JobSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        buildJobSetName(target),
			Namespace:   namespace,
			Labels:      jobSetLabels,
			Annotations: jobSetAnnotations,
			Finalizers:  []string{constants.JobSetFinalizer},
		},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: replicatedJobs,
			Suspend:        boolPtr(false),
			SuccessPolicy: &jobsetv1alpha2.SuccessPolicy{
				Operator: jobsetv1alpha2.OperatorAll,
			},
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
	return executionEnv.InjectOnJobSetCreation(ctx, workUnit, jobSet)
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

func boolPtr(b bool) *bool {
	return &b
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

func generateEnvVars(job *esspb.KubernetesJob) []corev1.EnvVar {
	return generateEnvVarsForSpec(job.GetSpec())
}

func generateEnvVarsForSpec(spec *esspb.ExecutableSpec) []corev1.EnvVar {
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
	envVars = append(envVars, corev1.EnvVar{Name: constants.XmcEnvVarManagedBy, Value: "true"})

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

func defaultVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "ram-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
		{
			Name: "disk-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumDefault,
				},
			},
		},
		{
			Name: "dev-shm-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
	}
}

func defaultVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "ram-scratch",
			MountPath: "/dev/ram-scratch",
		},
		{
			Name:      "disk-scratch",
			MountPath: "/dev/disk-scratch",
		},
		{
			Name:      "dev-shm-scratch",
			MountPath: "/dev/shm",
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
