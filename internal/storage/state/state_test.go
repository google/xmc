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
package state

import (
	"context"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/cmp/cmpopts/cmpopts"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

var (
	msgInfo = &essmsgpb.StructuredMessage{
		Severity: essmsgpb.StructuredMessage_INFO,
	}
	msgWarning = &essmsgpb.StructuredMessage{
		Severity: essmsgpb.StructuredMessage_WARNING,
	}
	msgError = &essmsgpb.StructuredMessage{
		Severity: essmsgpb.StructuredMessage_ERROR,
	}
)

func TestUpdateExperimentActiveState(t *testing.T) {
	testCases := []struct {
		name string
		exp  *essmsgpb.Experiment
		want essmsgpb.ExperimentStatus_ActiveState
	}{
		{
			name: "launch_failed",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_FAILED,
				},
			},
			want: essmsgpb.ExperimentStatus_INACTIVE,
		},
		{
			name: "launching",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
				},
			},
			want: essmsgpb.ExperimentStatus_ACTIVE,
		},
		{
			name: "launch_completed_no_work_units",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_COMPLETED,
				},
			},
			want: essmsgpb.ExperimentStatus_INACTIVE,
		},
		{
			name: "launch_completed_terminal_work_units",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_COMPLETED,
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_COMPLETED, Count: 1},
						{State: essmsgpb.WorkUnitStatus_FAILED, Count: 1},
					},
				},
			},
			want: essmsgpb.ExperimentStatus_INACTIVE,
		},
		{
			name: "launch_completed_non_terminal_work_units",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_COMPLETED,
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_COMPLETED, Count: 1},
						{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
					},
				},
			},
			want: essmsgpb.ExperimentStatus_ACTIVE,
		},
		{
			name: "launch_unspecified_non_terminal_work_units",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					LaunchState: essmsgpb.ExperimentStatus_LAUNCH_STATE_UNSPECIFIED,
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
					},
				},
			},
			want: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UpdateExperimentActiveState(tc.exp)
			if err != nil {
				t.Fatalf("UpdateExperimentActiveState failed: %v", err)
			}
			if got.GetStatus().GetActiveState() != tc.want {
				t.Errorf("UpdateExperimentActiveState returned active state %v, want %v", got.GetStatus().GetActiveState(), tc.want)
			}
		})
	}
}

func TestDenormalizeWorkUnitStatus(t *testing.T) {
	testCases := []struct {
		name               string
		exp                *essmsgpb.Experiment
		deltas             []*WorkUnitStateDelta
		wantStateCounts    []*essmsgpb.ExperimentStatus_StateCount
		wantSeverityCounts []*essmsgpb.ExperimentStatus_MessageSeverityCount
	}{
		{
			name: "no_deltas",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
					},
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
			},
		},
		{
			name: "new_work_unit",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState:   nil,
					NewState:       essmsgpb.WorkUnitStatus_INITIALIZING.Enum(),
					CurrentMessage: nil,
					NewMessage:     msgInfo,
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_INITIALIZING, Count: 1},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
			},
		},
		{
			name: "work_unit_state_change",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_INITIALIZING, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_INITIALIZING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_RUNNING.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
			},
		},
		{
			name: "work_unit_completed",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_RUNNING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_COMPLETED.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_COMPLETED, Count: 1},
			},
		},
		{
			name: "multiple_deltas",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 2},
						{State: essmsgpb.WorkUnitStatus_FAILED, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_RUNNING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_COMPLETED.Enum(),
				},
				{
					CurrentState: essmsgpb.WorkUnitStatus_RUNNING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_STOPPED.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_COMPLETED, Count: 1},
				{State: essmsgpb.WorkUnitStatus_FAILED, Count: 1},
				{State: essmsgpb.WorkUnitStatus_STOPPED, Count: 1},
			},
		},
		{
			name: "state_count_becomes_zero",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
						{State: essmsgpb.WorkUnitStatus_INITIALIZING, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_INITIALIZING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_RUNNING.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
			},
		},
		{
			name: "state_count_not_negative",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_INITIALIZING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_RUNNING.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_RUNNING, Count: 1},
			},
		},
		{
			name: "current_state_not_found_in_map",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					ChildStateCounts: []*essmsgpb.ExperimentStatus_StateCount{},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentState: essmsgpb.WorkUnitStatus_RUNNING.Enum(),
					NewState:     essmsgpb.WorkUnitStatus_COMPLETED.Enum(),
				},
			},
			wantStateCounts: []*essmsgpb.ExperimentStatus_StateCount{
				{State: essmsgpb.WorkUnitStatus_COMPLETED, Count: 1},
			},
		},
		{
			name: "severity_change_info_to_warning",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentMessage: msgInfo,
					NewMessage:     msgWarning,
				},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_WARNING, Count: 1},
			},
		},
		{
			name: "severity_change_warning_to_error_multiple",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_WARNING, Count: 2},
						{Severity: essmsgpb.StructuredMessage_ERROR, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentMessage: msgWarning,
					NewMessage:     msgError,
				},
				{
					CurrentMessage: msgWarning,
					NewMessage:     msgError,
				},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_ERROR, Count: 3},
			},
		},
		{
			name: "message_cleared",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentMessage: msgInfo,
					NewMessage:     nil,
				},
			},
			wantSeverityCounts: nil,
		},
		{
			name: "mix_of_severity_updates",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
						{Severity: essmsgpb.StructuredMessage_WARNING, Count: 2},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentMessage: msgInfo,
					NewMessage:     nil,
				},
				{
					CurrentMessage: msgWarning,
					NewMessage:     msgError,
				},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_WARNING, Count: 1},
				{Severity: essmsgpb.StructuredMessage_ERROR, Count: 1},
			},
		},
		{
			name: "severity_no_change",
			exp: &essmsgpb.Experiment{
				Status: &essmsgpb.ExperimentStatus{
					MessageSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
						{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
					},
				},
			},
			deltas: []*WorkUnitStateDelta{
				{
					CurrentMessage: msgInfo,
					NewMessage:     msgInfo,
				},
			},
			wantSeverityCounts: []*essmsgpb.ExperimentStatus_MessageSeverityCount{
				{Severity: essmsgpb.StructuredMessage_INFO, Count: 1},
			},
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DenormalizeWorkUnitStatus(ctx, tc.exp, tc.deltas)
			if err != nil {
				t.Fatalf("DenormalizeWorkUnitStatus failed: %v", err)
			}

			sortStateProtos := cmpopts.SortSlices(func(m1, m2 *essmsgpb.ExperimentStatus_StateCount) bool { return m1.GetState() < m2.GetState() })
			if diff := cmp.Diff(tc.wantStateCounts, got.GetStatus().GetChildStateCounts(), protocmp.Transform(), sortStateProtos); diff != "" {
				t.Errorf("DenormalizeWorkUnitStatus returned diff for state counts (-want +got):\n%s", diff)
			}

			sortSeverityProtos := cmpopts.SortSlices(func(m1, m2 *essmsgpb.ExperimentStatus_MessageSeverityCount) bool {
				return m1.GetSeverity() < m2.GetSeverity()
			})
			if diff := cmp.Diff(tc.wantSeverityCounts, got.GetStatus().GetMessageSeverityCounts(), protocmp.Transform(), sortSeverityProtos); diff != "" {
				t.Errorf("DenormalizeWorkUnitStatus returned diff for severity counts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestShouldUpdateWorkUnitExecutionState(t *testing.T) {
	now := time.Now()
	nowpb := timestamppb.New(now)
	pastpb := timestamppb.New(now.Add(-time.Minute))
	futurepb := timestamppb.New(now.Add(time.Minute))

	testCases := []struct {
		name             string
		currentExecution *essmsgpb.WorkUnitExecution
		newExecution     *essmsgpb.WorkUnitExecution
		want             bool
	}{
		{
			name: "state_unchanged",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "new_state_is_initializing",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_INITIALIZING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "new_state_is_restarting",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RESTARTING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "current_state_is_terminal_stopped",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPED,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "current_state_is_terminal_completed",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "current_state_is_terminal_failed",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_FAILED,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "state_update_time_is_zero",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_COMPLETED,
				},
			},
			want: false,
		},
		{
			name: "new_state_time_is_older",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: pastpb,
				},
			},
			want: false,
		},
		{
			name: "time_ignored_state_stopping",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPING,
					StateUpdateTime: pastpb,
				},
			},
			want: true,
		},
		{
			name: "valid_update_initializing_to_running",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_INITIALIZING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name: "valid_update_running_to_completed",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name: "valid_update_running_to_stopping",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPING,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name:             "current_execution_nil_status",
			currentExecution: &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name: "new_execution_nil_status_and_state_time",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"},
			want:         false,
		},
		{
			name: "new_state_time_is_same_as_current",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name: "new_state_time_is_more_recent_than_current",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: futurepb,
				},
			},
			want: true,
		},
		{
			name: "state_update_time_is_zero_from_unspecified",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			want: false,
		},
		{
			name: "valid_update_stopping_to_stopped",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPED,
					StateUpdateTime: nowpb,
				},
			},
			want: true,
		},
		{
			name: "invalid_update_stopping_to_running",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_STOPPING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "different_xid",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/2/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "different_wid",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/2/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
		{
			name: "different_eid",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_RUNNING,
					StateUpdateTime: pastpb,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/2",
				Status: &essmsgpb.WorkUnitStatus{
					State:           essmsgpb.WorkUnitStatus_COMPLETED,
					StateUpdateTime: nowpb,
				},
			},
			want: false,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShouldUpdateWorkUnitExecutionState(ctx, tc.currentExecution, tc.newExecution)
			if err != nil {
				t.Fatalf("ShouldUpdateWorkUnitExecutionState failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("ShouldUpdateWorkUnitExecutionState returned %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldStatusMessageSupercede(t *testing.T) {
	now := time.Now()
	nowpb := timestamppb.New(now)
	pastpb := timestamppb.New(now.Add(-time.Minute))
	futurepb := timestamppb.New(now.Add(time.Minute))

	testCases := []struct {
		name       string
		currentMsg *essmsgpb.StructuredMessage
		newMsg     *essmsgpb.StructuredMessage
		want       bool
	}{
		{
			name: "different_parent",
			currentMsg: &essmsgpb.StructuredMessage{
				Parent: "parent1",
			},
			newMsg: &essmsgpb.StructuredMessage{
				Parent: "parent2",
			},
			want: false,
		},
		{
			name: "lower_severity",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_ERROR,
				CreateTime: pastpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			want: false,
		},
		{
			name: "higher_severity",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: pastpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_ERROR,
				CreateTime: nowpb,
			},
			want: true,
		},
		{
			name: "higher_severity_and_older",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_ERROR,
				CreateTime: pastpb,
			},
			want: true,
		},
		{
			name: "update_time_is_zero",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: pastpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity: essmsgpb.StructuredMessage_INFO,
			},
			want: false,
		},
		{
			name: "new_time_is_older",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: pastpb,
			},
			want: false,
		},
		{
			name: "new_time_is_same",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			want: true,
		},
		{
			name: "new_time_is_newer",
			currentMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: pastpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: futurepb,
			},
			want: true,
		},
		{
			name: "does_not_supercede_with_lower_severity",
			currentMsg: &essmsgpb.StructuredMessage{
				Source:     essmsgpb.StructuredMessage_SCHEDULER,
				Severity:   essmsgpb.StructuredMessage_ERROR,
				CreateTime: pastpb,
			},
			newMsg: &essmsgpb.StructuredMessage{
				Source:     essmsgpb.StructuredMessage_SOURCE_UNSPECIFIED,
				Severity:   essmsgpb.StructuredMessage_INFO,
				CreateTime: nowpb,
			},
			want: false,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldStatusMessageSupercede(ctx, tc.currentMsg, tc.newMsg)
			if got != tc.want {
				t.Errorf("ShouldStatusMessageSupercede() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldResetStatusMessage(t *testing.T) {
	infoMsg := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO}
	errorMsg := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR}
	fatalMsg := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_FATAL}

	testCases := []struct {
		name             string
		currentExecution *essmsgpb.WorkUnitExecution
		newExecution     *essmsgpb.WorkUnitExecution
		want             bool
	}{
		{
			name: "state_unchanged",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: infoMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: infoMsg,
				},
			},
			want: false,
		},
		{
			name: "message_changed",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: infoMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_COMPLETED,
					SystemMessage: errorMsg,
				},
			},
			want: false,
		},
		{
			name: "current_message_nil",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: nil,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_COMPLETED,
					SystemMessage: nil,
				},
			},
			want: false,
		},
		{
			name: "current_message_fatal",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: fatalMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_COMPLETED,
					SystemMessage: fatalMsg,
				},
			},
			want: false,
		},
		{
			name: "retaining_state_stopped",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_STOPPING,
					SystemMessage: infoMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_STOPPED,
					SystemMessage: infoMsg,
				},
			},
			want: false,
		},
		{
			name: "retaining_state_failed",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: infoMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_FAILED,
					SystemMessage: infoMsg,
				},
			},
			want: false,
		},
		{
			name: "should_reset",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_RUNNING,
					SystemMessage: infoMsg,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State:         essmsgpb.WorkUnitStatus_COMPLETED,
					SystemMessage: infoMsg,
				},
			},
			want: true,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShouldResetStatusMessage(ctx, tc.currentExecution, tc.newExecution, tc.currentExecution.GetStatus().GetSystemMessage(), tc.newExecution.GetStatus().GetSystemMessage())
			if err != nil {
				t.Fatalf("ShouldResetStatusMessage() failed: %v", err)
			}
			if got != tc.want {
				t.Errorf("ShouldResetStatusMessage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldResetSchedulingInfo(t *testing.T) {
	testCases := []struct {
		name             string
		currentExecution *essmsgpb.WorkUnitExecution
		newExecution     *essmsgpb.WorkUnitExecution
		want             bool
	}{
		{
			name: "state_unchanged",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			want: false,
		},
		{
			name: "state_changed_to_scheduling",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_SCHEDULING,
				},
			},
			want: false,
		},
		{
			name: "state_changed_to_running",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_SCHEDULING,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			want: true,
		},
		{
			name: "new_status_nil",
			currentExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_SCHEDULING,
				},
			},
			newExecution: &essmsgpb.WorkUnitExecution{},
			want:         true,
		},
		{
			name:             "current_status_nil_new_status_running",
			currentExecution: &essmsgpb.WorkUnitExecution{},
			newExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
				},
			},
			want: true,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldResetSchedulingInfo(ctx, tc.currentExecution, tc.newExecution)
			if got != tc.want {
				t.Errorf("ShouldResetSchedulingInfo() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindBestStatusMessage(t *testing.T) {
	testCases := []struct {
		name       string
		candidates []*essmsgpb.StructuredMessage
		want       *essmsgpb.StructuredMessage
	}{
		{
			name:       "empty_list",
			candidates: []*essmsgpb.StructuredMessage{},
			want:       nil,
		},
		{
			name: "single_message",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "msg"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "msg"},
		},
		{
			name: "longest_message_is_first",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "long message"},
				{UserFacingText: "short"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "long message"},
		},
		{
			name: "longest_message_is_last",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "short"},
				{UserFacingText: "long message"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "long message"},
		},
		{
			name: "multiple_messages_with_same_length",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "abc"},
				{UserFacingText: "def"},
				{UserFacingText: "a"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "abc"},
		},
		{
			name: "empty_string_message",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: ""},
				{UserFacingText: "a"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "a"},
		},
		{
			name: "all_empty_string_messages",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: ""},
				{UserFacingText: ""},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: ""},
		},
		{
			name: "stack_trace_wins",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "short", AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "stack"}}},
				{UserFacingText: "long message"},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "short", AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "stack"}}},
		},
		{
			name: "longer_stack_trace_wins",
			candidates: []*essmsgpb.StructuredMessage{
				{UserFacingText: "a", AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "short stack"}}},
				{UserFacingText: "b", AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "a very long stack trace"}}},
			},
			want: &essmsgpb.StructuredMessage{UserFacingText: "b", AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "a very long stack trace"}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindBestStatusMessage(tc.candidates)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("FindBestStatusMessage(%v) returned diff (-want +got):\n%s", tc.candidates, diff)
			}
		})
	}
}

func TestGetBestMessageFromStatus(t *testing.T) {
	sysInfo := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "sys-info"}
	sysError := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "sys-error"}
	usrInfo := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "usr-info"}
	usrError := &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "usr-error"}

	testCases := []struct {
		name   string
		status *essmsgpb.WorkUnitStatus
		want   *essmsgpb.StructuredMessage
	}{
		{
			name:   "empty",
			status: &essmsgpb.WorkUnitStatus{},
			want:   nil,
		},
		{
			name:   "only_system",
			status: &essmsgpb.WorkUnitStatus{SystemMessage: sysInfo},
			want:   sysInfo,
		},
		{
			name:   "only_user",
			status: &essmsgpb.WorkUnitStatus{UserMessage: usrInfo},
			want:   usrInfo,
		},
		{
			name:   "system_higher_severity",
			status: &essmsgpb.WorkUnitStatus{SystemMessage: sysError, UserMessage: usrInfo},
			want:   sysError,
		},
		{
			name:   "user_higher_severity",
			status: &essmsgpb.WorkUnitStatus{SystemMessage: sysInfo, UserMessage: usrError},
			want:   usrError,
		},
		{
			name:   "tie_system_longer",
			status: &essmsgpb.WorkUnitStatus{SystemMessage: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "longer"}, UserMessage: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "short"}},
			want:   &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "longer"},
		},
		{
			name:   "tie_user_longer",
			status: &essmsgpb.WorkUnitStatus{SystemMessage: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "short"}, UserMessage: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "longer"}},
			want:   &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "longer"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetBestMessageFromStatus(tc.status)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetBestMessageFromStatus(%+v) returned diff (-want +got):\n%s", tc.status, diff)
			}
		})
	}
}
