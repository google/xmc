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
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"xmanager_cloud/internal/logging"
	ee "xmanager_cloud/internal/toollibs/execution_environments"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/jobsetutil"
	"xmanager_cloud/validator/validator"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

var defaultLabels = map[string]string{
	constants.ManagedByLabel: constants.XmcLabelValue,
}

func (c *Client) k8sJobToReplicatedJob(ctx context.Context, job *esspb.KubernetesJob, jobLabels map[string]string, jobAnnotations map[string]string) jobsetv1alpha2.ReplicatedJob {
	jobMeta := buildJobMetadata(job, jobLabels, jobAnnotations)
	retryLimit, restartPolicy, pfp := resolveJobRetryAndFailurePolicy(job)

	return jobsetv1alpha2.ReplicatedJob{
		Name:      job.GetName(),
		GroupName: job.GetGroupName(),
		Replicas:  job.GetReplicas(),
		DependsOn: buildJobDependsOn(ctx, job),
		Template: batchv1.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      jobMeta.labels,
				Annotations: jobMeta.annotations,
			},
			Spec: batchv1.JobSpec{
				BackoffLimit:            &retryLimit,
				CompletionMode:          ptr.To(batchv1.IndexedCompletion),
				Completions:             ptr.To(int32(1)),
				PodFailurePolicy:        pfp,
				TTLSecondsAfterFinished: ptr.To(int32(defaultTTLSeconds)),
				ActiveDeadlineSeconds:   optionalPositiveInt64Ptr(job.GetActiveDeadlineSeconds()),
				Template:                c.buildReplicatedJobPodTemplate(job, jobMeta, restartPolicy),
			},
		},
	}
}

func (c *Client) buildReplicatedJobPodTemplate(job *esspb.KubernetesJob, jobMeta jobMetadata, restartPolicy corev1.RestartPolicy) corev1.PodTemplateSpec {
	hostNetwork, dnsPolicy := resolveDNSAndNetwork(c, job)
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      jobMeta.podLabels,
			Annotations: jobMeta.podAnnotations,
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
			NodeSelector:                  copyNodeSelector(job.GetNodeSelector()),
			TerminationGracePeriodSeconds: optionalNonNegativeInt64Ptr(job.TerminationGracePeriodSeconds),
		},
	}
}

func buildJobDependsOn(ctx context.Context, job *esspb.KubernetesJob) []jobsetv1alpha2.DependsOn {
	depOrder := job.GetDependencyOrder()
	if depOrder.GetJobName() == "" {
		return nil
	}
	return []jobsetv1alpha2.DependsOn{
		{
			Name:   depOrder.GetJobName(),
			Status: dependencyStatus(ctx, depOrder.GetJobDependencyState()),
		},
	}
}

func optionalPositiveInt64Ptr(val int64) *int64 {
	if val <= 0 {
		return nil
	}
	return ptr.To(val)
}

func optionalNonNegativeInt64Ptr(val *int64) *int64 {
	if val == nil || *val < 0 {
		return nil
	}
	return ptr.To(*val)
}

func resolveJobRetryAndFailurePolicy(job *esspb.KubernetesJob) (int32, corev1.RestartPolicy, *batchv1.PodFailurePolicy) {
	retryLimit := job.GetRetryLimit()
	restartPolicy := corev1.RestartPolicyNever
	if job.GetRestartPolicy() != "" {
		restartPolicy = corev1.RestartPolicy(job.GetRestartPolicy())
	}
	if restartPolicy == corev1.RestartPolicyOnFailure {
		if retryLimit == 0 {
			retryLimit = constants.DefaultOnFailureRetryLimit
		}
		return retryLimit, restartPolicy, nil
	}
	if job.GetDisablePodFailurePolicy() {
		return retryLimit, restartPolicy, nil
	}
	return retryLimit, restartPolicy, podFailurePolicy(jobsetutil.PrimaryContainerName(job))
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
	mergeEntriesPreservingExisting(labels, job.GetLabels())

	return jobMetadata{
		labels:         labels,
		annotations:    annotations,
		podLabels:      maps.Clone(labels),
		podAnnotations: maps.Clone(annotations),
	}
}

func mergeEntriesPreservingExisting(dst, src map[string]string) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
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
	jobSetLabels := maps.Clone(jobLabels)
	jobSetLabels[constants.LastEditLabel] = strconv.FormatInt(operationTime.Unix(), 10)
	jobSetLabels[kueuePriorityClassLabel] = getPriorityClassName(priorities)

	defaultAnnotations := map[string]string{
		constants.AuthorAnnotation:                author,
		constants.AutoscalerSafeToEvictAnnotation: "false",
	}
	jobSetAnnotations := maps.Clone(defaultAnnotations)
	if jobset := workUnit.GetExecutable().GetJobset(); jobset != nil {
		mergeEntriesPreservingExisting(jobSetAnnotations, jobset.GetAnnotations())
		mergeEntriesPreservingExisting(jobSetLabels, jobset.GetLabels())
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
	xprofObjectPath := c.resolveManagedXprofObjectPath(ctx, xid, wid, operationTime)
	replicatedJobs := c.buildReplicatedJobs(ctx, workUnit, meta.jobLabels, meta.defaultAnnotations, xprofObjectPath)
	jobSet := buildBaseJobSet(workUnit, buildJobSetName(target), namespace, meta, replicatedJobs)

	return c.finalizeAndValidateJobSet(ctx, workUnit, namespace, jobSet)
}

// resolveManagedXprofObjectPath returns the managed-xprof GCS destination whenever the cluster
// configures a managed-xprof GCS bucket. This is intentionally decoupled from the ManagedXprof
// artifact: the workload only needs to know where to write xprof output, regardless of whether or
// when the artifact is attached.
func (c *Client) resolveManagedXprofObjectPath(ctx context.Context, xid, wid string, operationTime time.Time) string {
	bucket, err := c.ManagedXProfGCSBucket(ctx)
	if err != nil {
		logging.Warn(ctx, "Failed to resolve managed-xprof GCS path via Actuator", "error", err)
		return ""
	}
	if bucket == "" {
		return ""
	}
	return fmt.Sprintf("%s/xmc-%s-%s-%s", strings.TrimSuffix(bucket, "/"), xid, wid, strconv.FormatInt(operationTime.Unix(), 10))
}

func (c *Client) buildReplicatedJobs(ctx context.Context, workUnit *esspb.WorkUnit, jobLabels, defaultAnnotations map[string]string, xprofObjectPath string) []jobsetv1alpha2.ReplicatedJob {
	if workUnit.GetExecutable().GetJobset() == nil {
		return nil
	}
	var replicatedJobs []jobsetv1alpha2.ReplicatedJob
	for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
		if job != nil {
			rj := c.k8sJobToReplicatedJob(ctx, job, jobLabels, defaultAnnotations)
			injectManagedXprofGCSEnvVars(&rj, xprofObjectPath)
			replicatedJobs = append(replicatedJobs, rj)
		}
	}
	return replicatedJobs
}

func buildBaseJobSet(workUnit *esspb.WorkUnit, jobSetName, namespace string, meta jobSetLabelsAndAnnotations, replicatedJobs []jobsetv1alpha2.ReplicatedJob) *jobsetv1alpha2.JobSet {
	return &jobsetv1alpha2.JobSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: jobsetv1alpha2.GroupVersion.String(),
			Kind:       "JobSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobSetName,
			Namespace:   namespace,
			Labels:      meta.jobSetLabels,
			Annotations: meta.jobSetAnnotations,
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
}

func (c *Client) finalizeAndValidateJobSet(ctx context.Context, workUnit *esspb.WorkUnit, namespace string, jobSet *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error) {
	executionEnv, ok := ee.Lookup(c.RuntimeConfig.DefaultExecutionEnvironment)
	if !ok {
		logging.Error(ctx, "Failed to get execution environment", "execution_environment", c.RuntimeConfig.DefaultExecutionEnvironment)
		return nil, fmt.Errorf("failed to get execution environment: %q", c.RuntimeConfig.DefaultExecutionEnvironment)
	}
	jobSet, err := executionEnv.InjectOnJobSetCreation(ctx, workUnit, jobSet)
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

func buildJobSetName(target string) string {
	ctx := logging.WithWorkUnitExecutionName(context.Background(), target)
	xid, wid, _, err := validator.ValidateWorkUnitExecutionName(target)
	if err != nil {
		logging.Warn(ctx, "Failed to extract IDs from target", "target", target, "error", err)
		return ""
	}
	return fmt.Sprintf("xmc-operation-%d-%d", xid, wid)
}
