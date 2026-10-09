// Copyright 2026 Google LLC
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
	"slices"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	constants "xmanager_cloud/kubernetes/common/constants"
)

// copyNodeSelector creates an immutable copy of the job node selector map.
func copyNodeSelector(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	return maps.Clone(source)
}

func resolveDNSAndNetwork(c *Client, job *esspb.KubernetesJob) (hostNetwork bool, dnsPolicy corev1.DNSPolicy) {
	if c != nil && c.RuntimeConfig.AllowHostNetwork && job.GetHostNetwork() {
		if job.GetDnsPolicy() != "" {
			return true, corev1.DNSPolicy(job.GetDnsPolicy())
		}
		return true, corev1.DNSClusterFirstWithHostNet
	}
	if job.GetDnsPolicy() != "" {
		return false, corev1.DNSPolicy(job.GetDnsPolicy())
	}
	return false, ""
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
		EnableDNSHostnames:       ptr.To(true),
		Subdomain:                subdomain,
		PublishNotReadyAddresses: ptr.To(publishNotReady),
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
	return slices.ContainsFunc(jobs, func(job *esspb.KubernetesJob) bool {
		return job.GetName() == name
	})
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
					{Type: "DisruptionTarget"},
				},
			},
			// Allow users to throw an error code for early exit.
			exitCodeFailureRule(batchv1.PodFailurePolicyActionFailJob, containerName, 50),
			// Allow users to throw an error code that doesn't count against the backoff limit.
			exitCodeFailureRule(batchv1.PodFailurePolicyActionIgnore, containerName, 42),
		},
	}
}

func exitCodeFailureRule(action batchv1.PodFailurePolicyAction, containerName string, code int32) batchv1.PodFailurePolicyRule {
	return batchv1.PodFailurePolicyRule{
		Action: action,
		OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
			ContainerName: ptr.To(containerName),
			Operator:      "In",
			Values:        []int32{code},
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
