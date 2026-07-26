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
package jobset

import (
	"strings"
	"testing"
	"time"

	"golang/cmp/cmp"
	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
	storagepkg "xmanager_cloud/storage/storage"

	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
)

func TestToWorkUnitState(t *testing.T) {
	testCases := []struct {
		name   string
		jobSet *v1alpha2.JobSet
		want   spb.WorkUnitStatus_WorkUnitState
	}{
		{
			name: "Completed",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionTrue},
					},
				},
			},
			want: spb.WorkUnitStatus_COMPLETED,
		},
		{
			name: "Failed",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetFailed), Status: metav1.ConditionTrue},
					},
				},
			},
			want: spb.WorkUnitStatus_FAILED,
		},
		{
			name: "Completed condition False does not affect status",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionFalse},
					},
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Ready: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Failed condition False does not affect status",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetFailed), Status: metav1.ConditionFalse},
					},
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Ready: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Completed condition takes precedence",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionTrue},
					},
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Failed: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_COMPLETED,
		},
		{
			name: "Failed condition takes precedence",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					Suspend: boolPtr(true),
				},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetFailed), Status: metav1.ConditionTrue},
					},
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_FAILED,
		},
		{
			name: "Stopped - DeletionTimestamp set",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			want: spb.WorkUnitStatus_STOPPED,
		},
		{
			name: "Partially Failed",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Failed: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_PARTIALLY_FAILED,
		},
		{
			name: "Running",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Ready: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Running - not all pods ready",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 2},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 2, Ready: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Starting",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Ready: 0},
					},
				},
			},
			want: spb.WorkUnitStatus_STARTING,
		},
		{
			name: "Running - finishing",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 2},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 1, Ready: 0, Succeeded: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Running - partially succeeded",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 2},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 0, Ready: 0, Succeeded: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name: "Scheduling - StartupPolicyInProgress",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetStartupPolicyInProgress), Status: metav1.ConditionTrue},
					},
				},
			},
			want: spb.WorkUnitStatus_SCHEDULING,
		},
		{
			name: "Scheduling - No Active, Not Complete",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Active: 0, Failed: 0, Succeeded: 0},
					},
				},
			},
			want: spb.WorkUnitStatus_SCHEDULING,
		},
		{
			name: "All succeeded but no Completed condition",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "test", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "test", Succeeded: 1},
					},
				},
			},
			want: spb.WorkUnitStatus_RUNNING,
		},
		{
			name:   "Empty JobSet",
			jobSet: &v1alpha2.JobSet{},
			want:   spb.WorkUnitStatus_SCHEDULING,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToWorkUnitState(tc.jobSet)
			if got != tc.want {
				t.Errorf("ToWorkUnitState() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCalculateJobTotals(t *testing.T) {
	testCases := []struct {
		name              string
		jobSet            *v1alpha2.JobSet
		wantSucceeded     int32
		wantFailed        int32
		wantActive        int32
		wantReady         int32
		wantTotalReplicas int32
	}{
		{
			name: "Multiple replicated jobs",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "rj1", Replicas: 2},
						{Name: "rj2", Replicas: 3},
					},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "rj1", Succeeded: 1, Failed: 0, Active: 1, Ready: 1},
						{Name: "rj2", Succeeded: 0, Failed: 1, Active: 2, Ready: 1},
					},
				},
			},
			wantSucceeded:     1,
			wantFailed:        1,
			wantActive:        3,
			wantReady:         2,
			wantTotalReplicas: 5,
		},
		{
			name: "No status",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{Name: "rj1", Replicas: 1},
					},
				},
				Status: v1alpha2.JobSetStatus{},
			},
			wantSucceeded:     0,
			wantFailed:        0,
			wantActive:        0,
			wantReady:         0,
			wantTotalReplicas: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotSucceeded, gotFailed, gotActive, gotReady, gotTotalReplicas := calculateJobTotals(tc.jobSet)
			if gotSucceeded != tc.wantSucceeded || gotFailed != tc.wantFailed || gotActive != tc.wantActive || gotReady != tc.wantReady || gotTotalReplicas != tc.wantTotalReplicas {
				t.Errorf("calculateJobTotals() = (%d, %d, %d, %d, %d), want (%d, %d, %d, %d, %d)",
					gotSucceeded, gotFailed, gotActive, gotReady, gotTotalReplicas,
					tc.wantSucceeded, tc.wantFailed, tc.wantActive, tc.wantReady, tc.wantTotalReplicas)
			}
		})
	}
}

func TestGenerateStructuredMessage(t *testing.T) {
	tests := []struct {
		name              string
		jobSet            *v1alpha2.JobSet
		state             spb.WorkUnitStatus_WorkUnitState
		podErrorMsg       string
		startupWarningMsg string
		want              *spb.StructuredMessage
	}{
		{
			name: "RunningState",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 2}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 2, Ready: 2}},
				},
			},
			state: spb.WorkUnitStatus_RUNNING,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is running. 2/2 job(s) active and ready.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "FailedCondition",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{Name: "failed-jobset", Namespace: "default"},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(v1alpha2.JobSetFailed),
							Status:  metav1.ConditionTrue,
							Message: "Something went wrong",
							Reason:  "ErrorABC",
						},
					},
				},
			},
			state: spb.WorkUnitStatus_FAILED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit failed: Something went wrong (Reason: ErrorABC)",
				UserFacingUrl: &spb.Url{
					Url: "https://console.cloud.google.com/kubernetes/aiml/jobset/test-project/GKE_CLUSTER/test-location/test-cluster/default/failed-jobset/overview?project=test-project",
				},
				Severity: spb.StructuredMessage_ERROR,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "CompletedCondition",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha2.JobSetCompleted),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			state: spb.WorkUnitStatus_COMPLETED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit completed successfully.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "CompletedConditionWithMessage",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(v1alpha2.JobSetCompleted),
							Status:  metav1.ConditionTrue,
							Message: "Finished in 10s",
						},
					},
				},
			},
			state: spb.WorkUnitStatus_COMPLETED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit completed successfully with message: Finished in 10s",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "RunningStateNotAllReady",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 2}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 2, Ready: 1}},
				},
			},
			state: spb.WorkUnitStatus_RUNNING,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is running. 2/2 job(s) active, 1 ready.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "StartingState",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 1}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 1, Ready: 0}},
				},
			},
			state: spb.WorkUnitStatus_STARTING,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is starting. 1/1 job(s) active, 0 ready.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "StartingStateWithStartupWarning",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 1}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 1, Ready: 0}},
				},
			},
			state:             spb.WorkUnitStatus_STARTING,
			startupWarningMsg: "Work unit may be unschedulable: 0/3 nodes are available.",
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit may be unschedulable: 0/3 nodes are available.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_WARNING,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "PartiallyFailedState",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{Name: "partially-failed-jobset", Namespace: "default"},
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 3}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Failed: 1, Active: 1, Succeeded: 1}},
				},
			},
			state: spb.WorkUnitStatus_PARTIALLY_FAILED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is partially failed. 1 failed, 1 active, 1 succeeded out of 3 job(s).",
				UserFacingUrl: &spb.Url{
					Url: "https://console.cloud.google.com/kubernetes/aiml/jobset/test-project/GKE_CLUSTER/test-location/test-cluster/default/partially-failed-jobset/overview?project=test-project",
				},
				Severity: spb.StructuredMessage_WARNING,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name:   "SchedulingState",
			jobSet: &v1alpha2.JobSet{},
			state:  spb.WorkUnitStatus_SCHEDULING,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is waiting for resources from the scheduler.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_SCHEDULER,
			},
		},
		{
			name: "SchedulingStateWithMessage",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(v1alpha2.JobSetStartupPolicyInProgress),
							Status:  metav1.ConditionTrue,
							Message: "Startup policy in progress",
						},
					},
				},
			},
			state: spb.WorkUnitStatus_SCHEDULING,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is scheduling jobs: Startup policy in progress",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_SCHEDULER,
			},
		},
		{
			name:   "StoppedState",
			jobSet: &v1alpha2.JobSet{},
			state:  spb.WorkUnitStatus_STOPPED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit has been fully stopped.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name:   "UnspecifiedState",
			jobSet: &v1alpha2.JobSet{},
			state:  spb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit state is unspecified. This should not happen.",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_DEBUG,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "FailedState_MissingProject",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{Name: "failed-jobset", Namespace: "default"},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(v1alpha2.JobSetFailed),
							Status:  metav1.ConditionTrue,
							Message: "Something went wrong",
							Reason:  "ErrorABC",
						},
					},
				},
			},
			state: spb.WorkUnitStatus_FAILED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit failed: Something went wrong (Reason: ErrorABC)",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_ERROR,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "PartiallyFailedState_MissingLocation",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{Name: "partially-failed-jobset", Namespace: "default"},
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 3}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Failed: 1, Active: 1, Succeeded: 1}},
				},
			},
			state: spb.WorkUnitStatus_PARTIALLY_FAILED,
			want: &spb.StructuredMessage{
				UserFacingText: "Work unit is partially failed. 1 failed, 1 active, 1 succeeded out of 3 job(s).",
				UserFacingUrl: &spb.Url{
					Url: "",
				},
				Severity: spb.StructuredMessage_WARNING,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
		{
			name: "StartingStateWithPodError",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-jobset-pod-error",
					Namespace: "default",
				},
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 1}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 1, Ready: 0}},
				},
			},
			state:       spb.WorkUnitStatus_FAILED,
			podErrorMsg: "Job is stuck in ImagePullBackOff",
			want: &spb.StructuredMessage{
				UserFacingText: "Job is stuck in ImagePullBackOff",
				UserFacingUrl: &spb.Url{
					Url: "https://console.cloud.google.com/kubernetes/aiml/jobset/test-project/GKE_CLUSTER/test-location/test-cluster/default/test-jobset-pod-error/overview?project=test-project",
				},
				Severity: spb.StructuredMessage_ERROR,
				Source:   spb.StructuredMessage_KUBERNETES,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := "test-project"
			location := "test-location"
			clusterName := "test-cluster"

			// Modify project, location, or clusterName based on test case name
			if strings.Contains(tc.name, "MissingProject") {
				project = ""
			}
			if strings.Contains(tc.name, "MissingLocation") {
				location = ""
			}
			if strings.Contains(tc.name, "MissingClusterName") {
				clusterName = ""
			}

			time := time.Now()

			got := generateStructuredMessage(t.Context(), tc.jobSet, tc.state, project, location, clusterName, tc.podErrorMsg, tc.startupWarningMsg)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform(), protocmp.IgnoreFields(&spb.StructuredMessage{}, "create_time")); diff != "" {
				t.Errorf("generateStructuredStatusMessage() returned diff (-want +got):\n%s", diff)
			}

			if got.GetCreateTime().AsTime().Compare(time) < 0 {
				t.Errorf("generateStructuredStatusMessage() returned create_time %v, want after %v", got.GetCreateTime().AsTime(), time)
			}
		})
	}
}

func TestConvertJobSetToWorkUnitExecutionUpdate(t *testing.T) {
	tests := []struct {
		name              string
		jobSet            *v1alpha2.JobSet
		wid               int64
		eid               int64
		podErrorMsg       string
		startupWarningMsg string
		want              *storagepkg.WorkUnitExecutionUpdate
		wantErr           bool
	}{
		{
			name: "successful conversion",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-jobset",
					Namespace: "default",
					Labels:    map[string]string{constants.WorkUnitIDLabel: "123"},
				},
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											ServiceAccountName: "test-sa",
										},
									},
								},
							},
						},
					},
				},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionTrue}},
					Restarts:   1,
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
						{Name: "worker", Active: 0, Succeeded: 1},
					},
				},
			},
			wid: 123,
			eid: 456,
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID:                    123,
				EID:                    456,
				DeferToExistingMessage: false,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Status: &spb.WorkUnitStatus{
						State: spb.WorkUnitStatus_COMPLETED,
						SystemMessage: &spb.StructuredMessage{
							UserFacingText: "Work unit completed successfully.",
							UserFacingUrl: &spb.Url{
								Url: "",
							},
							Severity: spb.StructuredMessage_DEBUG,
							Source:   spb.StructuredMessage_KUBERNETES,
						},
						DetailedStatus: &spb.ExecutableStatus{
							Kind: &spb.ExecutableStatus_JobsetStatus{
								JobsetStatus: &spb.JobSetStatus{
									Conditions: []*spb.Condition{
										{
											Type:       "Completed",
											Status:     "True",
											UpdateTime: timestamppb.New(time.Time{}),
										},
									},
									Restarts: 1,
									ReplicatedJobsStatus: []*spb.ReplicatedJobStatus{
										{Name: "worker", Active: 0, Succeeded: 1},
									},
								},
							},
						},
					},
					Metadata: &spb.WorkUnitExecutionMetadata{
						ManagementCluster: &spb.ClusterInfo{
							Project:     "test-project",
							Location:    "test-location",
							ClusterName: "test-cluster",
						},
						Type:           spb.WorkUnitExecutionMetadata_JOB_SET,
						ResourceName:   "test-jobset",
						Namespace:      "default",
						ServiceAccount: "test-sa",
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"status.state", "status.detailed_status", "status.system_message", "metadata.management_cluster", "metadata.type", "metadata.resource_name", "metadata.namespace", "metadata.service_account"}},
			},
			wantErr: false,
		},
		{
			name: "failed conversion",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-jobset-failed",
					Namespace: "default",
					Labels:    map[string]string{constants.WorkUnitIDLabel: "123"},
				},
				Spec: v1alpha2.JobSetSpec{},
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{{Type: string(v1alpha2.JobSetFailed), Status: metav1.ConditionTrue, Message: "jobset failed", Reason: "reason"}},
					Restarts:   1,
				},
			},
			wid: 123,
			eid: 456,
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID:                    123,
				EID:                    456,
				DeferToExistingMessage: true,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Status: &spb.WorkUnitStatus{
						State: spb.WorkUnitStatus_FAILED,
						SystemMessage: &spb.StructuredMessage{
							UserFacingText: "Work unit failed: jobset failed (Reason: reason)",
							UserFacingUrl: &spb.Url{
								Url: "https://console.cloud.google.com/kubernetes/aiml/jobset/test-project/GKE_CLUSTER/test-location/test-cluster/default/test-jobset-failed/overview?project=test-project",
							},
							Severity: spb.StructuredMessage_ERROR,
							Source:   spb.StructuredMessage_KUBERNETES,
						},
						DetailedStatus: &spb.ExecutableStatus{
							Kind: &spb.ExecutableStatus_JobsetStatus{
								JobsetStatus: &spb.JobSetStatus{
									Conditions: []*spb.Condition{
										{
											Type:       "Failed",
											Status:     "True",
											UpdateTime: timestamppb.New(time.Time{}),
											Message:    "jobset failed",
											Reason:     "reason",
										},
									},
									Restarts: 1,
								},
							},
						},
					},
					Metadata: &spb.WorkUnitExecutionMetadata{ManagementCluster: &spb.ClusterInfo{Project: "test-project", Location: "test-location", ClusterName: "test-cluster"}, Type: spb.WorkUnitExecutionMetadata_JOB_SET, ResourceName: "test-jobset-failed", Namespace: "default"},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"status.state", "status.detailed_status", "status.system_message", "metadata.management_cluster", "metadata.type", "metadata.resource_name", "metadata.namespace", "metadata.service_account"}},
			},
		},
		{
			name: "conversion_with_pod_error",
			jobSet: &v1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-jobset-error",
					Namespace: "default",
					Labels:    map[string]string{constants.WorkUnitIDLabel: "123"},
				},
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 1}},
				},
				Status: v1alpha2.JobSetStatus{
					ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Active: 1, Ready: 0}},
				},
			},
			wid:         123,
			eid:         456,
			podErrorMsg: "Job is stuck in ImagePullBackOff",
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID:                    123,
				EID:                    456,
				DeferToExistingMessage: true,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Status: &spb.WorkUnitStatus{
						State: spb.WorkUnitStatus_FAILED,
						SystemMessage: &spb.StructuredMessage{
							UserFacingText: "Job is stuck in ImagePullBackOff",
							UserFacingUrl: &spb.Url{
								Url: "https://console.cloud.google.com/kubernetes/aiml/jobset/test-project/GKE_CLUSTER/test-location/test-cluster/default/test-jobset-error/overview?project=test-project",
							},
							Severity: spb.StructuredMessage_ERROR,
							Source:   spb.StructuredMessage_KUBERNETES,
						},
						DetailedStatus: &spb.ExecutableStatus{
							Kind: &spb.ExecutableStatus_JobsetStatus{
								JobsetStatus: &spb.JobSetStatus{
									ReplicatedJobsStatus: []*spb.ReplicatedJobStatus{
										{Active: 1, Ready: 0},
									},
								},
							},
						},
					},
					Metadata: &spb.WorkUnitExecutionMetadata{ManagementCluster: &spb.ClusterInfo{Project: "test-project", Location: "test-location", ClusterName: "test-cluster"}, Type: spb.WorkUnitExecutionMetadata_JOB_SET, ResourceName: "test-jobset-error", Namespace: "default"},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"status.state", "status.detailed_status", "status.system_message", "metadata.management_cluster", "metadata.type", "metadata.resource_name", "metadata.namespace", "metadata.service_account"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			time := time.Now()

			got, err := ConvertJobSetToWorkUnitExecutionUpdate(t.Context(), tc.jobSet, tc.wid, tc.eid, "test-project", "test-location", "test-cluster", tc.podErrorMsg, tc.startupWarningMsg)
			if (err != nil) != tc.wantErr {
				t.Errorf("ConvertJobSetToWorkUnitExecutionUpdate() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform(), protocmp.IgnoreFields(&spb.StructuredMessage{}, "create_time"), protocmp.IgnoreFields(&spb.WorkUnitStatus{}, "state_update_time")); diff != "" {
				t.Errorf("ConvertJobSetToWorkUnitExecutionUpdate() returned diff (-want +got):\n%s", diff)
			}

			if got.WorkUnitExecution.GetStatus().GetSystemMessage().GetCreateTime().AsTime().Before(time) {
				t.Errorf("ConvertJobSetToWorkUnitExecutionUpdate() returned create_time %v, want after %v", got.WorkUnitExecution.GetStatus().GetSystemMessage().GetCreateTime().AsTime(), time)
			}

			if got.WorkUnitExecution.GetStatus().GetStateUpdateTime().AsTime().Before(time) {
				t.Errorf("ConvertJobSetToWorkUnitExecutionUpdate() returned state_update_time %v, want after %v", got.WorkUnitExecution.GetStatus().GetStateUpdateTime().AsTime(), time)
			}
		})
	}
}

func TestConvertK8sJobSetStatusToProto(t *testing.T) {
	now := time.Unix(1700000000, 0)
	nowProto := timestamppb.New(now)

	tests := []struct {
		name      string
		k8sStatus v1alpha2.JobSetStatus
		want      *spb.JobSetStatus
	}{
		{
			name: "all fields",
			k8sStatus: v1alpha2.JobSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "type1",
						Status:             "status1",
						LastTransitionTime: metav1.Time{Time: now},
						Reason:             "reason1",
						Message:            "message1",
					},
				},
				Restarts: 2,
				ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
					{
						Name:      "rj1",
						Ready:     1,
						Succeeded: 2,
						Failed:    3,
						Active:    4,
					},
				},
			},
			want: &spb.JobSetStatus{
				Conditions: []*spb.Condition{
					{
						Type:       "type1",
						Status:     "status1",
						UpdateTime: nowProto,
						Reason:     "reason1",
						Message:    "message1",
					},
				},
				Restarts: 2,
				ReplicatedJobsStatus: []*spb.ReplicatedJobStatus{
					{
						Name:      "rj1",
						Ready:     1,
						Succeeded: 2,
						Failed:    3,
						Active:    4,
					},
				},
			},
		},
		{
			name:      "empty",
			k8sStatus: v1alpha2.JobSetStatus{},
			want:      &spb.JobSetStatus{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertK8sJobSetStatusToProto(tc.k8sStatus)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("convertK8sJobSetStatusToProto() returned diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetMLRunURL(t *testing.T) {
	const runURL = "https://mlrun.example/run-1"
	tests := []struct {
		name       string
		update     *storagepkg.WorkUnitExecutionUpdate
		mlRunURL   string
		wantURL    string
		wantInMask bool
	}{
		{
			name:       "nil metadata is instantiated",
			update:     &storagepkg.WorkUnitExecutionUpdate{WorkUnitExecution: &spb.WorkUnitExecution{}, UpdateMask: &fmpb.FieldMask{}},
			mlRunURL:   runURL,
			wantURL:    runURL,
			wantInMask: true,
		},
		{
			name:       "existing metadata is reused",
			update:     &storagepkg.WorkUnitExecutionUpdate{WorkUnitExecution: &spb.WorkUnitExecution{Metadata: &spb.WorkUnitExecutionMetadata{}}, UpdateMask: &fmpb.FieldMask{}},
			mlRunURL:   runURL,
			wantURL:    runURL,
			wantInMask: true,
		},
		{
			name:       "empty url is a no-op",
			update:     &storagepkg.WorkUnitExecutionUpdate{WorkUnitExecution: &spb.WorkUnitExecution{}, UpdateMask: &fmpb.FieldMask{}},
			mlRunURL:   "",
			wantURL:    "",
			wantInMask: false,
		},
		{
			name:       "nil update mask still sets url",
			update:     &storagepkg.WorkUnitExecutionUpdate{WorkUnitExecution: &spb.WorkUnitExecution{}},
			mlRunURL:   runURL,
			wantURL:    runURL,
			wantInMask: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setMLRunURL(tc.update, tc.mlRunURL)
			got := ""
			if tc.update.WorkUnitExecution.Metadata != nil {
				got = tc.update.WorkUnitExecution.Metadata.MlrunUrl
			}
			if got != tc.wantURL {
				t.Errorf("MlrunUrl = %q, want %q", got, tc.wantURL)
			}
			inMask := false
			if tc.update.UpdateMask != nil {
				for _, p := range tc.update.UpdateMask.Paths {
					if p == "metadata.mlrun_url" {
						inMask = true
					}
				}
			}
			if inMask != tc.wantInMask {
				t.Errorf("mask has metadata.mlrun_url = %v, want %v", inMask, tc.wantInMask)
			}
		})
	}
}
