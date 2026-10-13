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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
)

func TestConstructJobSet_PodFailurePolicy(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := baseCPUWorkUnit

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	pfp := got.Spec.ReplicatedJobs[0].Template.Spec.PodFailurePolicy
	if pfp == nil {
		t.Fatal("Expected PodFailurePolicy to be set, but it was nil")
	}

	if len(pfp.Rules) != 3 {
		t.Fatalf("Expected 3 PodFailurePolicy rules, got %d", len(pfp.Rules))
	}

	t.Run("Rule 1: DisruptionTarget", func(t *testing.T) {
		rule1 := pfp.Rules[0]
		if rule1.Action != batchv1.PodFailurePolicyActionIgnore {
			t.Errorf("Rule 1 Action: got %v, want %v", rule1.Action, batchv1.PodFailurePolicyActionIgnore)
		}
		if len(rule1.OnPodConditions) != 1 || rule1.OnPodConditions[0].Type != "DisruptionTarget" {
			t.Errorf("Rule 1 OnPodConditions: got %v, want DisruptionTarget", rule1.OnPodConditions)
		}
	})

	t.Run("Rule 2: Exit Code 50 -> FailJob", func(t *testing.T) {
		rule2 := pfp.Rules[1]
		if rule2.Action != batchv1.PodFailurePolicyActionFailJob {
			t.Errorf("Rule 2 Action: got %v, want %v", rule2.Action, batchv1.PodFailurePolicyActionFailJob)
		}
		if rule2.OnExitCodes == nil {
			t.Fatal("Rule 2 OnExitCodes is nil")
		}
		if *rule2.OnExitCodes.ContainerName != "job1" {
			t.Errorf("Rule 2 ContainerName: got %q, want %q", *rule2.OnExitCodes.ContainerName, "job1")
		}
		if len(rule2.OnExitCodes.Values) != 1 || rule2.OnExitCodes.Values[0] != 50 {
			t.Errorf("Rule 2 Values: got %v, want [50]", rule2.OnExitCodes.Values)
		}
	})

	t.Run("Rule 3: Exit Code 42 -> Ignore", func(t *testing.T) {
		rule3 := pfp.Rules[2]
		if rule3.Action != batchv1.PodFailurePolicyActionIgnore {
			t.Errorf("Rule 3 Action: got %v, want %v", rule3.Action, batchv1.PodFailurePolicyActionIgnore)
		}
		if rule3.OnExitCodes == nil {
			t.Fatal("Rule 3 OnExitCodes is nil")
		}
		if *rule3.OnExitCodes.ContainerName != "job1" {
			t.Errorf("Rule 3 ContainerName: got %q, want %q", *rule3.OnExitCodes.ContainerName, "job1")
		}
		if len(rule3.OnExitCodes.Values) != 1 || rule3.OnExitCodes.Values[0] != 42 {
			t.Errorf("Rule 3 Values: got %v, want [42]", rule3.OnExitCodes.Values)
		}
	})
}

func TestConstructJobSet_PodFailurePolicy_Disabled(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].DisablePodFailurePolicy = proto.Bool(true)
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	pfp := got.Spec.ReplicatedJobs[0].Template.Spec.PodFailurePolicy
	if pfp != nil {
		t.Errorf("Expected PodFailurePolicy to be nil when disable_pod_failure_policy=true, got %+v", pfp)
	}
}

func TestConstructJobSet_OnFailureRestartPolicy(t *testing.T) {
	tests := []struct {
		name        string
		retryLimit  int32
		wantBackoff int32
	}{
		{
			name:        "default backoff limit when retry_limit is 0",
			retryLimit:  0,
			wantBackoff: constants.DefaultOnFailureRetryLimit,
		},
		{
			name:        "custom retry limit",
			retryLimit:  5,
			wantBackoff: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
				wu.Executable.GetJobset().Jobs[0].RestartPolicy = proto.String("OnFailure")
				wu.Executable.GetJobset().Jobs[0].RetryLimit = tt.retryLimit
			})

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}

			if len(got.Spec.ReplicatedJobs) != 1 {
				t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
			}

			rjSpec := got.Spec.ReplicatedJobs[0].Template.Spec
			if rjSpec.PodFailurePolicy != nil {
				t.Errorf("Expected PodFailurePolicy to be nil for OnFailure restart policy, got %+v", rjSpec.PodFailurePolicy)
			}
			if rjSpec.BackoffLimit == nil || *rjSpec.BackoffLimit != tt.wantBackoff {
				t.Errorf("Expected BackoffLimit to be %d, got %v", tt.wantBackoff, rjSpec.BackoffLimit)
			}
			if rjSpec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
				t.Errorf("Expected RestartPolicy to be OnFailure, got %v", rjSpec.Template.Spec.RestartPolicy)
			}
		})
	}
}

func TestConstructJobSet_RestartPolicy(t *testing.T) {
	tests := []struct {
		name       string
		val        *string
		wantPolicy corev1.RestartPolicy
	}{
		{name: "default when unset", val: nil, wantPolicy: corev1.RestartPolicyNever},
		{name: "explicit Never", val: proto.String("Never"), wantPolicy: corev1.RestartPolicyNever},
		{name: "explicit OnFailure", val: proto.String("OnFailure"), wantPolicy: corev1.RestartPolicyOnFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
				wu.Executable.GetJobset().Jobs[0].RestartPolicy = tt.val
			})

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}

			if len(got.Spec.ReplicatedJobs) != 1 {
				t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
			}

			gotPodSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			if gotPodSpec.RestartPolicy != tt.wantPolicy {
				t.Errorf("Expected RestartPolicy to be %q, got %q", tt.wantPolicy, gotPodSpec.RestartPolicy)
			}
		})
	}
}

func TestConstructJobSet_DependencyOrder(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job1 := wu.Executable.GetJobset().Jobs[0]
		job2 := prototest.Modify(job1, func(j *esspb.KubernetesJob) {
			j.Name = "job2"
			j.DependencyOrder = &esspb.KubernetesJob_DependencyOrder{
				JobName:            "job1",
				JobDependencyState: esspb.KubernetesJob_DependencyOrder_READY,
			}
		})
		job3 := prototest.Modify(job1, func(j *esspb.KubernetesJob) {
			j.Name = "job3"
			j.DependencyOrder = &esspb.KubernetesJob_DependencyOrder{
				JobName:            "job2",
				JobDependencyState: esspb.KubernetesJob_DependencyOrder_COMPLETED,
			}
		})
		job4 := prototest.Modify(job1, func(j *esspb.KubernetesJob) {
			j.Name = "job4"
			j.DependencyOrder = &esspb.KubernetesJob_DependencyOrder{
				JobName:            "job3",
				JobDependencyState: esspb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED,
			}
		})
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{job1, job2, job3, job4}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 4 {
		t.Fatalf("Expected 4 replicated jobs, got %d", len(got.Spec.ReplicatedJobs))
	}

	// Check job1 (no dependencies)
	if len(got.Spec.ReplicatedJobs[0].DependsOn) != 0 {
		t.Errorf("Expected job1 to have no dependencies, got %v", got.Spec.ReplicatedJobs[0].DependsOn)
	}

	// Check job2 (depends on job1 being READY)
	if len(got.Spec.ReplicatedJobs[1].DependsOn) != 1 {
		t.Fatalf("Expected job2 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[1].DependsOn))
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Name != "job1" {
		t.Errorf("Expected job2 to depend on job1, got %s", got.Spec.ReplicatedJobs[1].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Status != jobsetv1alpha2.DependencyReady {
		t.Errorf("Expected job2 to depend on job1 with status READY, got %v", got.Spec.ReplicatedJobs[1].DependsOn[0].Status)
	}

	// Check job3 (depends on job2 being COMPLETED)
	if len(got.Spec.ReplicatedJobs[2].DependsOn) != 1 {
		t.Fatalf("Expected job3 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[2].DependsOn))
	}
	if got.Spec.ReplicatedJobs[2].DependsOn[0].Name != "job2" {
		t.Errorf("Expected job3 to depend on job2, got %s", got.Spec.ReplicatedJobs[2].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[2].DependsOn[0].Status != jobsetv1alpha2.DependencyComplete {
		t.Errorf("Expected job3 to depend on job2 with status COMPLETED, got %v", got.Spec.ReplicatedJobs[2].DependsOn[0].Status)
	}

	// Check job4 (depends on job3 with UNSPECIFIED status)
	if len(got.Spec.ReplicatedJobs[3].DependsOn) != 1 {
		t.Fatalf("Expected job4 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[3].DependsOn))
	}
	if got.Spec.ReplicatedJobs[3].DependsOn[0].Name != "job3" {
		t.Errorf("Expected job4 to depend on job3, got %s", got.Spec.ReplicatedJobs[3].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[3].DependsOn[0].Status != jobsetv1alpha2.DependencyReady {
		t.Errorf("Expected job4 to depend on job3 with status READY, got %v", got.Spec.ReplicatedJobs[3].DependsOn[0].Status)
	}
}

func TestConstructJobSet_DependencyOrder_PathwaysHeadAndWorker(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		head := wu.Executable.GetJobset().Jobs[0]
		head.Name = "pathways-head"
		worker := prototest.Modify(head, func(j *esspb.KubernetesJob) {
			j.Name = "worker"
			j.Replicas = 2
			j.DependencyOrder = &esspb.KubernetesJob_DependencyOrder{
				JobName:            "pathways-head",
				JobDependencyState: esspb.KubernetesJob_DependencyOrder_READY,
			}
		})
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{head, worker}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 2 {
		t.Fatalf("Expected 2 replicated jobs, got %d", len(got.Spec.ReplicatedJobs))
	}

	// Verify pathways-head has no dependencies
	if len(got.Spec.ReplicatedJobs[0].DependsOn) != 0 {
		t.Errorf("Expected pathways-head to have no dependencies, got %v", got.Spec.ReplicatedJobs[0].DependsOn)
	}

	// Verify worker depends on pathways-head being READY
	if len(got.Spec.ReplicatedJobs[1].DependsOn) != 1 {
		t.Fatalf("Expected worker to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[1].DependsOn))
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Name != "pathways-head" {
		t.Errorf("Expected worker to depend on pathways-head, got %s", got.Spec.ReplicatedJobs[1].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Status != jobsetv1alpha2.DependencyReady {
		t.Errorf("Expected worker to depend on pathways-head with status READY, got %v", got.Spec.ReplicatedJobs[1].DependsOn[0].Status)
	}
}

func TestConstructJobSet_GPU_MultiReplica_WithCoordinator(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		js := wu.Executable.GetJobset()
		js.Coordinator = &esspb.JobSetExecutable_Coordinator{
			ReplicatedJob: "job1",
			JobIndex:      proto.Int32(2),
			PodIndex:      proto.Int32(0),
		}
		js.Jobs[0].Replicas = 4
		js.Jobs[0].Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_GpuResources{
				GpuResources: &esspb.GpuResources{Type: "H100", Count: 8},
			},
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Coordinator == nil {
		t.Fatalf("Expected Coordinator to be set, got nil")
	}
	if got.Spec.Coordinator.JobIndex != 0 {
		t.Errorf("Expected Coordinator.JobIndex to be mapped to 0, got %d", got.Spec.Coordinator.JobIndex)
	}
	if got.Spec.Coordinator.PodIndex != 2 {
		t.Errorf("Expected Coordinator.PodIndex to be mapped to 2, got %d", got.Spec.Coordinator.PodIndex)
	}
}

func TestConstructJobSet_DefaultNetworkMultiJob(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitMulti"
		job1 := wu.Executable.GetJobset().Jobs[0]
		job2 := proto.Clone(job1).(*esspb.KubernetesJob)
		job2.Name = "job2"
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{job1, job2}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated for multi-job WorkUnit, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
	if got.Spec.Network.PublishNotReadyAddresses == nil || !*got.Spec.Network.PublishNotReadyAddresses {
		t.Errorf("Expected PublishNotReadyAddresses to be true, got %v", got.Spec.Network.PublishNotReadyAddresses)
	}
	wantSubdomain := got.Name
	if got.Spec.Network.Subdomain != wantSubdomain {
		t.Errorf("Subdomain mismatch: got %q, want %q", got.Spec.Network.Subdomain, wantSubdomain)
	}
}

func TestConstructJobSet_SingleJobNoNetwork(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitSingle"
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network != nil {
		t.Errorf("Expected Spec.Network to be nil for single-replica single-job WorkUnit, got %+v", got.Spec.Network)
	}
}

func TestConstructJobSet_ExplicitNetworkOverride(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	enableDNS := true
	publishNotReady := false
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitOverride"
		wu.Executable.GetJobset().Network = &esspb.JobSetExecutable_Network{
			EnableDnsHostnames:       &enableDNS,
			Subdomain:                "custom-subdomain",
			PublishNotReadyAddresses: &publishNotReady,
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated when explicitly set, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
	if got.Spec.Network.Subdomain != "custom-subdomain" {
		t.Errorf("Subdomain mismatch: got %q, want %q", got.Spec.Network.Subdomain, "custom-subdomain")
	}
	if got.Spec.Network.PublishNotReadyAddresses == nil || *got.Spec.Network.PublishNotReadyAddresses {
		t.Errorf("Expected PublishNotReadyAddresses to be false, got %v", got.Spec.Network.PublishNotReadyAddresses)
	}
}

func TestConstructJobSet_MultiReplicaNetwork(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitMultiReplica"
		wu.Executable.GetJobset().Jobs[0].Replicas = 2
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated for multi-replica WorkUnit, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
}

func TestShouldEnableJobSetNetwork_NilChecks(t *testing.T) {
	if got := shouldEnableJobSetNetwork(&esspb.WorkUnit{Executable: nil}); got {
		t.Errorf("shouldEnableJobSetNetwork(nil Executable) = true, want false")
	}
	if got := shouldEnableJobSetNetwork(&esspb.WorkUnit{Executable: &esspb.Executable{}}); got {
		t.Errorf("shouldEnableJobSetNetwork(nil Jobset) = true, want false")
	}
}

func TestShouldEnableJobSetNetwork_Conditions(t *testing.T) {
	trueVal, falseVal := true, false
	tests := []struct {
		name string
		wu   *esspb.WorkUnit
		want bool
	}{
		{
			name: "explicit_true",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Network: &esspb.JobSetExecutable_Network{EnableDnsHostnames: &trueVal},
			}}}},
			want: true,
		},
		{
			name: "explicit_false",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Network: &esspb.JobSetExecutable_Network{EnableDnsHostnames: &falseVal},
				Jobs:    []*esspb.KubernetesJob{{Replicas: 2}},
			}}}},
			want: false,
		},
		{
			name: "multi_job",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Jobs: []*esspb.KubernetesJob{{Name: "j1"}, {Name: "j2"}},
			}}}},
			want: true,
		},
		{
			name: "multi_replica",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Jobs: []*esspb.KubernetesJob{{Replicas: 2}},
			}}}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnableJobSetNetwork(tc.wu); got != tc.want {
				t.Errorf("shouldEnableJobSetNetwork() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildJobSetSuccessPolicy(t *testing.T) {
	tests := []struct {
		name string
		wu   *esspb.WorkUnit
		want *jobsetv1alpha2.SuccessPolicy
	}{
		{"nil_exec", &esspb.WorkUnit{}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}},
		{"explicit_any", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{SuccessPolicy: &esspb.JobSetExecutable_SuccessPolicy{Operator: esspb.JobSetExecutable_SuccessPolicy_ANY, TargetReplicatedJobs: []string{"w"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAny, TargetReplicatedJobs: []string{"w"}}},
		{"default_pathways", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Jobs: []*esspb.KubernetesJob{{Name: constants.PathwaysHeadJobName}, {Name: "workers"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll, TargetReplicatedJobs: []string{constants.PathwaysHeadJobName}}},
		{"fallback_all", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Jobs: []*esspb.KubernetesJob{{Name: "job1"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, buildJobSetSuccessPolicy(tc.wu)); diff != "" {
				t.Errorf("buildJobSetSuccessPolicy() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConstructJobSet_SuccessPolicy(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Name = constants.PathwaysHeadJobName
	})
	jobSet, err := client.constructJobSet(ctx, workUnit, "experiments/1/workUnits/1/executions/1", "user", "test-queue", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() unexpected error: %v", err)
	}
	want := &jobsetv1alpha2.SuccessPolicy{
		Operator:             jobsetv1alpha2.OperatorAll,
		TargetReplicatedJobs: []string{constants.PathwaysHeadJobName},
	}
	if diff := cmp.Diff(want, jobSet.Spec.SuccessPolicy); diff != "" {
		t.Errorf("SuccessPolicy mismatch (-want +got):\n%s", diff)
	}
}

func TestConstructJobSet_WithCoordinator(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitWithCoordinator"
		wu.Executable.GetJobset().Coordinator = &esspb.JobSetExecutable_Coordinator{
			ReplicatedJob: "head",
			JobIndex:      proto.Int32(0),
			PodIndex:      proto.Int32(0),
		}
		headJob := wu.Executable.GetJobset().Jobs[0]
		headJob.Name = "head"
		headJob.Replicas = 1

		workerJob := proto.Clone(headJob).(*esspb.KubernetesJob)
		workerJob.Name = "worker"
		workerJob.Replicas = 4

		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{headJob, workerJob}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Coordinator == nil {
		t.Fatalf("Expected Spec.Coordinator to be populated, got nil")
	}
	if got.Spec.Coordinator.ReplicatedJob != "head" {
		t.Errorf("ReplicatedJob mismatch: got %q, want %q", got.Spec.Coordinator.ReplicatedJob, "head")
	}
	if got.Spec.Coordinator.JobIndex != 0 {
		t.Errorf("JobIndex mismatch: got %d, want %d", got.Spec.Coordinator.JobIndex, 0)
	}
	if got.Spec.Coordinator.PodIndex != 0 {
		t.Errorf("PodIndex mismatch: got %d, want %d", got.Spec.Coordinator.PodIndex, 0)
	}
}

func TestShouldEnableJobSetCoordinator(t *testing.T) {
	if got := shouldEnableJobSetCoordinator(nil); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: nil}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Executable) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Jobset) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: nil}}}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Coordinator) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: &esspb.JobSetExecutable_Coordinator{ReplicatedJob: ""}}}}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(empty ReplicatedJob) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: &esspb.JobSetExecutable_Coordinator{ReplicatedJob: "worker"}}}}}); !got {
		t.Errorf("shouldEnableJobSetCoordinator(valid Coordinator) = false, want true")
	}
}

func TestValidateNonRestartingJobSetContract(t *testing.T) {
	newJobSetWithPolicy := func(maxRestarts int32, rp corev1.RestartPolicy) *jobsetv1alpha2.JobSet {
		return &jobsetv1alpha2.JobSet{
			Spec: jobsetv1alpha2.JobSetSpec{
				FailurePolicy: &jobsetv1alpha2.FailurePolicy{MaxRestarts: maxRestarts},
				ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
					{
						Name: "job1",
						Template: batchv1.JobTemplateSpec{
							Spec: batchv1.JobSpec{
								Template: corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{RestartPolicy: rp},
								},
							},
						},
					},
				},
			},
		}
	}

	t.Run("Valid JobSet contract", func(t *testing.T) {
		if err := validateNonRestartingJobSetContract(newJobSetWithPolicy(0, corev1.RestartPolicyNever)); err != nil {
			t.Errorf("validateNonRestartingJobSetContract() returned unexpected error: %v", err)
		}
	})

	t.Run("Contract violation: MaxRestarts > 0", func(t *testing.T) {
		if err := validateNonRestartingJobSetContract(newJobSetWithPolicy(2, corev1.RestartPolicyNever)); err == nil {
			t.Errorf("validateNonRestartingJobSetContract() expected error for MaxRestarts > 0, got nil")
		}
	})

	t.Run("Contract violation: Pod RestartPolicy Always", func(t *testing.T) {
		if err := validateNonRestartingJobSetContract(newJobSetWithPolicy(0, corev1.RestartPolicyAlways)); err == nil {
			t.Errorf("validateNonRestartingJobSetContract() expected error for RestartPolicyAlways, got nil")
		}
	})

	t.Run("Valid Pod RestartPolicy OnFailure", func(t *testing.T) {
		if err := validateNonRestartingJobSetContract(newJobSetWithPolicy(0, corev1.RestartPolicyOnFailure)); err != nil {
			t.Errorf("validateNonRestartingJobSetContract() unexpected error for RestartPolicyOnFailure: %v", err)
		}
	})
}

func TestHostNetworkAndDNSPolicy(t *testing.T) {
	tests := []struct {
		name             string
		allowHostNetwork bool
		specHostNetwork  *bool
		specDNSPolicy    *string
		wantHostNetwork  bool
		wantDNSPolicy    corev1.DNSPolicy
	}{
		{
			name:             "Flag enabled, host_network true, dns_policy unset -> defaults to ClusterFirstWithHostNet",
			allowHostNetwork: true,
			specHostNetwork:  proto.Bool(true),
			specDNSPolicy:    nil,
			wantHostNetwork:  true,
			wantDNSPolicy:    corev1.DNSClusterFirstWithHostNet,
		},
		{
			name:             "Flag enabled, host_network true, dns_policy explicit ClusterFirst",
			allowHostNetwork: true,
			specHostNetwork:  proto.Bool(true),
			specDNSPolicy:    proto.String("ClusterFirst"),
			wantHostNetwork:  true,
			wantDNSPolicy:    corev1.DNSClusterFirst,
		},
		{
			name:             "Flag disabled, host_network true -> hostNetwork is false",
			allowHostNetwork: false,
			specHostNetwork:  proto.Bool(true),
			specDNSPolicy:    nil,
			wantHostNetwork:  false,
			wantDNSPolicy:    "",
		},
		{
			name:             "Flag disabled, host_network true, dns_policy explicit -> hostNetwork is false, dnsPolicy set",
			allowHostNetwork: false,
			specHostNetwork:  proto.Bool(true),
			specDNSPolicy:    proto.String("ClusterFirst"),
			wantHostNetwork:  false,
			wantDNSPolicy:    corev1.DNSClusterFirst,
		},
		{
			name:             "Flag enabled, host_network false -> hostNetwork is false",
			allowHostNetwork: true,
			specHostNetwork:  proto.Bool(false),
			specDNSPolicy:    nil,
			wantHostNetwork:  false,
			wantDNSPolicy:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			client.RuntimeConfig.AllowHostNetwork = tt.allowHostNetwork

			workUnit := proto.Clone(baseCPUWorkUnit).(*esspb.WorkUnit)
			job := workUnit.GetExecutable().GetJobset().GetJobs()[0]
			job.HostNetwork = tt.specHostNetwork
			if tt.specDNSPolicy != nil {
				job.DnsPolicy = tt.specDNSPolicy
			}

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}

			podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			if podSpec.HostNetwork != tt.wantHostNetwork {
				t.Errorf("podSpec.HostNetwork = %v, want %v", podSpec.HostNetwork, tt.wantHostNetwork)
			}
			if podSpec.DNSPolicy != tt.wantDNSPolicy {
				t.Errorf("podSpec.DNSPolicy = %q, want %q", podSpec.DNSPolicy, tt.wantDNSPolicy)
			}
		})
	}
}
