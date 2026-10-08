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
package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "protobuf/timestamp_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestMergeWorkUnitExecution(t *testing.T) {
	ctx := context.Background()
	currentTime := time.Now()
	fatalMessage := &pb.StructuredMessage{
		UserFacingText: "fatal-structured-status-message",
		Severity:       pb.StructuredMessage_FATAL,
		CreateTime:     timestamppb.New(currentTime.Add(-time.Hour)),
	}
	current := &pb.WorkUnitExecution{
		Name: "experiments/1/workUnits/1/executions/1",
		Status: &pb.WorkUnitStatus{
			State:           pb.WorkUnitStatus_RUNNING,
			UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
			StateUpdateTime: timestamppb.New(currentTime.Add(-time.Hour)),
			SystemMessage: &pb.StructuredMessage{
				UserFacingText: "current-structured-status-message",
				CreateTime:     timestamppb.New(currentTime.Add(-time.Hour)),
			},
			UserMessage: &pb.StructuredMessage{
				UserFacingText: "current-user-message",
				CreateTime:     timestamppb.New(currentTime.Add(-time.Hour)),
			},
			DetailedStatus: &pb.ExecutableStatus{},
		},
		Metadata: &pb.WorkUnitExecutionMetadata{
			ManagementCluster: &pb.ClusterInfo{
				Project:     "current-project",
				Location:    "current-location",
				ClusterName: "current-cluster",
			},
			ExecutingCluster: &pb.ClusterInfo{
				Project:     "current-executing-project",
				Location:    "current-executing-location",
				ClusterName: "current-executing-cluster",
			},
			Type:           pb.WorkUnitExecutionMetadata_JOB_SET,
			ResourceName:   "current-resource-name",
			Namespace:      "current-namespace",
			ServiceAccount: "current-service-account",
			NodeSelectors:  map[string]string{"key1": "value1"},
			ResourceFlavor: "current-resource-flavor",
		},
	}
	updated := &pb.WorkUnitExecution{
		Name: "experiments/1/workUnits/1/executions/1",
		Status: &pb.WorkUnitStatus{
			State:           pb.WorkUnitStatus_COMPLETED,
			UpdateTime:      timestamppb.New(currentTime),
			StateUpdateTime: timestamppb.New(currentTime),
			SystemMessage: &pb.StructuredMessage{
				UserFacingText: "updated-structured-status-message",
				CreateTime:     timestamppb.New(currentTime),
			},
			UserMessage: &pb.StructuredMessage{
				UserFacingText: "updated-user-message",
				CreateTime:     timestamppb.New(currentTime),
			},
			DetailedStatus: &pb.ExecutableStatus{},
			SchedulingInformation: &pb.StructuredMessage{
				UserFacingText: "updated-admission-decision",
			},
		},
		Metadata: &pb.WorkUnitExecutionMetadata{
			ManagementCluster: &pb.ClusterInfo{
				Project:     "updated-project",
				Location:    "updated-location",
				ClusterName: "updated-cluster",
			},
			ExecutingCluster: &pb.ClusterInfo{
				Project:     "updated-executing-project",
				Location:    "updated-executing-location",
				ClusterName: "updated-executing-cluster",
			},
			Type:           pb.WorkUnitExecutionMetadata_JOB_SET,
			ResourceName:   "updated-resource-name",
			Namespace:      "updated-namespace",
			ServiceAccount: "updated-service-account",
			NodeSelectors:  map[string]string{"key2": "value2"},
			ResourceFlavor: "updated-resource-flavor",
		},
	}

	testCases := []struct {
		name                           string
		mask                           *fmpb.FieldMask
		want                           *pb.WorkUnitExecution
		wantErr                        bool
		useFatalMessage                bool
		useNilMessage                  bool
		changeUpdatedStateToFailed     bool
		changeCurrentStateToStopping   bool
		changeUpdatedStateToStopped    bool
		changeUpdatedStateToScheduling bool
	}{
		{
			name: "update status",
			mask: &fmpb.FieldMask{Paths: []string{"status"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:                 pb.WorkUnitStatus_COMPLETED,
					UpdateTime:            timestamppb.New(currentTime),
					StateUpdateTime:       timestamppb.New(currentTime),
					SystemMessage:         updated.Status.GetSystemMessage(),
					UserMessage:           updated.Status.GetUserMessage(),
					DetailedStatus:        &pb.ExecutableStatus{},
					SchedulingInformation: nil,
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update metadata",
			mask: &fmpb.FieldMask{Paths: []string{"metadata"}},
			want: &pb.WorkUnitExecution{
				Name:     "experiments/1/workUnits/1/executions/1",
				Status:   current.Status,
				Metadata: updated.Metadata,
			},
		},
		{
			name: "update status.state",
			mask: &fmpb.FieldMask{Paths: []string{"status.state"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_COMPLETED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   nil,
					UserMessage:     nil,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update status.state with fatal message",
			mask: &fmpb.FieldMask{Paths: []string{"status.state"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_COMPLETED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   fatalMessage,
					UserMessage:     nil,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
			useFatalMessage: true,
		},
		{
			name: "update status.state to failed",
			mask: &fmpb.FieldMask{Paths: []string{"status.state"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_FAILED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   current.Status.SystemMessage,
					UserMessage:     current.Status.UserMessage,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
			changeUpdatedStateToFailed: true,
		},
		{
			name: "update status.state from stopping to stopped",
			mask: &fmpb.FieldMask{Paths: []string{"status.state"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_STOPPED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   current.Status.SystemMessage,
					UserMessage:     current.Status.UserMessage,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
			changeCurrentStateToStopping: true,
			changeUpdatedStateToStopped:  true,
		},
		{
			name: "update status.state and status.system_message clears user_message",
			mask: &fmpb.FieldMask{Paths: []string{"status.state", "status.system_message"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_COMPLETED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   updated.Status.SystemMessage,
					UserMessage:     nil,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update status.state with nil message",
			mask: &fmpb.FieldMask{Paths: []string{"status.state"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_COMPLETED,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   nil,
					UserMessage:     nil,
					DetailedStatus:  &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
			useNilMessage: true,
		},
		{
			name: "update metadata.management_cluster.project",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.management_cluster.project"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: &pb.ClusterInfo{
						Project:     "updated-project",
						Location:    "current-location",
						ClusterName: "current-cluster",
					},
					ExecutingCluster: current.Metadata.ExecutingCluster,
					Type:             pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:     "current-resource-name",
					Namespace:        "current-namespace",
					ServiceAccount:   "current-service-account",
					NodeSelectors:    current.Metadata.NodeSelectors,
					ResourceFlavor:   current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.executing_cluster",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  updated.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.node_selectors",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.node_selectors"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     updated.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.resource_flavor",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.resource_flavor"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    "updated-resource-flavor",
				},
			},
		},
		{
			name: "update metadata.type",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.type"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.resource_name",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.resource_name"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "updated-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.namespace",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.namespace"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "updated-namespace",
					ServiceAccount:    "current-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update metadata.service_account",
			mask: &fmpb.FieldMask{Paths: []string{"metadata.service_account"}},
			want: &pb.WorkUnitExecution{
				Name:   "experiments/1/workUnits/1/executions/1",
				Status: current.Status,
				Metadata: &pb.WorkUnitExecutionMetadata{
					ManagementCluster: current.Metadata.ManagementCluster,
					ExecutingCluster:  current.Metadata.ExecutingCluster,
					Type:              pb.WorkUnitExecutionMetadata_JOB_SET,
					ResourceName:      "current-resource-name",
					Namespace:         "current-namespace",
					ServiceAccount:    "updated-service-account",
					NodeSelectors:     current.Metadata.NodeSelectors,
					ResourceFlavor:    current.Metadata.ResourceFlavor,
				},
			},
		},
		{
			name: "update status.system_message",
			mask: &fmpb.FieldMask{Paths: []string{"status.system_message"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_RUNNING,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime.Add(-time.Hour)),
					SystemMessage: &pb.StructuredMessage{
						UserFacingText: "updated-structured-status-message",
						CreateTime:     timestamppb.New(currentTime),
					},
					UserMessage:    current.Status.UserMessage,
					DetailedStatus: &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update status.user_message",
			mask: &fmpb.FieldMask{Paths: []string{"status.user_message"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_RUNNING,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime.Add(-time.Hour)),
					SystemMessage: &pb.StructuredMessage{
						UserFacingText: "current-structured-status-message",
						CreateTime:     timestamppb.New(currentTime.Add(-time.Hour)),
					},
					UserMessage: &pb.StructuredMessage{
						UserFacingText: "updated-user-message",
						CreateTime:     timestamppb.New(currentTime),
					},
					DetailedStatus: &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update status.detailed_status",
			mask: &fmpb.FieldMask{Paths: []string{"status.detailed_status"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_RUNNING,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime.Add(-time.Hour)),
					SystemMessage: &pb.StructuredMessage{
						UserFacingText: "current-structured-status-message",
						CreateTime:     timestamppb.New(currentTime.Add(-time.Hour)),
					},
					UserMessage:    current.Status.UserMessage,
					DetailedStatus: &pb.ExecutableStatus{},
				},
				Metadata: current.Metadata,
			},
		},
		{
			name: "update status.scheduling_information (which also requires the state to be SCHEDULING)",
			mask: &fmpb.FieldMask{Paths: []string{"status.state", "status.scheduling_information"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_SCHEDULING,
					UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   nil,
					UserMessage:     nil,
					DetailedStatus:  &pb.ExecutableStatus{},
					SchedulingInformation: &pb.StructuredMessage{
						UserFacingText: "updated-admission-decision",
					},
				},
				Metadata: current.Metadata,
			},
			changeUpdatedStateToScheduling: true,
		},
		{
			name:    "invalid field in mask",
			mask:    &fmpb.FieldMask{Paths: []string{"invalid_field"}},
			wantErr: true,
		},
		{
			name: "update status to scheduling retains scheduling info",
			mask: &fmpb.FieldMask{Paths: []string{"status"}},
			want: &pb.WorkUnitExecution{
				Name: "experiments/1/workUnits/1/executions/1",
				Status: &pb.WorkUnitStatus{
					State:           pb.WorkUnitStatus_SCHEDULING,
					UpdateTime:      timestamppb.New(currentTime),
					StateUpdateTime: timestamppb.New(currentTime),
					SystemMessage:   updated.Status.GetSystemMessage(),
					UserMessage:     updated.Status.GetUserMessage(),
					DetailedStatus:  &pb.ExecutableStatus{},
					SchedulingInformation: &pb.StructuredMessage{
						UserFacingText: "updated-admission-decision",
					},
				},
				Metadata: current.Metadata,
			},
			changeUpdatedStateToScheduling: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			currentClone := proto.Clone(current).(*pb.WorkUnitExecution)
			if tc.useFatalMessage {
				currentClone.Status.SystemMessage = fatalMessage
			}
			if tc.useNilMessage {
				currentClone.Status.SystemMessage = nil
			}
			if tc.changeCurrentStateToStopping {
				currentClone.Status.State = pb.WorkUnitStatus_STOPPING
			}
			updatedClone := proto.Clone(updated).(*pb.WorkUnitExecution)
			if tc.changeUpdatedStateToFailed {
				updatedClone.Status.State = pb.WorkUnitStatus_FAILED
			}
			if tc.changeUpdatedStateToStopped {
				updatedClone.Status.State = pb.WorkUnitStatus_STOPPED
			}
			if tc.changeUpdatedStateToScheduling {
				updatedClone.Status.State = pb.WorkUnitStatus_SCHEDULING
			}
			got, err := MergeWorkUnitExecution(ctx, currentClone, updatedClone, tc.mask, false, false)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeWorkUnitExecution() succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("MergeWorkUnitExecution() failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeWorkUnitExecution() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMergeWorkUnitExecution_InvalidArgument(t *testing.T) {
	ctx := context.Background()
	currentTime := time.Now()
	current := &pb.WorkUnitExecution{
		Name: "experiments/1/workUnits/1/executions/1",
		Status: &pb.WorkUnitStatus{
			State:           pb.WorkUnitStatus_RUNNING,
			UpdateTime:      timestamppb.New(currentTime.Add(-time.Hour)),
			StateUpdateTime: timestamppb.New(currentTime.Add(-time.Hour)),
		},
		Metadata: &pb.WorkUnitExecutionMetadata{},
	}
	updated := &pb.WorkUnitExecution{
		Name: "experiments/1/workUnits/1/executions/1",
		Status: &pb.WorkUnitStatus{
			State:           pb.WorkUnitStatus_COMPLETED,
			UpdateTime:      timestamppb.New(currentTime),
			StateUpdateTime: timestamppb.New(currentTime),
		},
		Metadata: &pb.WorkUnitExecutionMetadata{},
	}
	updatedWithoutStateUpdateTime := proto.Clone(updated).(*pb.WorkUnitExecution)
	updatedWithoutStateUpdateTime.Status.StateUpdateTime = nil

	testCases := []struct {
		name             string
		currentExecution *pb.WorkUnitExecution
		updatedExecution *pb.WorkUnitExecution
		mask             *fmpb.FieldMask
	}{
		{
			name:             "nil current execution",
			currentExecution: nil,
			updatedExecution: updated,
			mask:             &fmpb.FieldMask{Paths: []string{"status"}},
		},
		{
			name:             "nil updated execution",
			currentExecution: current,
			updatedExecution: nil,
			mask:             &fmpb.FieldMask{Paths: []string{"status"}},
		},
		{
			name:             "empty mask",
			currentExecution: current,
			updatedExecution: updated,
			mask:             &fmpb.FieldMask{Paths: []string{}},
		},
		{
			name: "nil state update time",
			currentExecution: &pb.WorkUnitExecution{
				Status: &pb.WorkUnitStatus{},
			},
			updatedExecution: updated,
			mask:             &fmpb.FieldMask{Paths: []string{"status"}},
		},
		{
			name:             "nil state update time in status.state update",
			currentExecution: current,
			updatedExecution: updatedWithoutStateUpdateTime,
			mask:             &fmpb.FieldMask{Paths: []string{"status.state"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MergeWorkUnitExecution(ctx, tc.currentExecution, tc.updatedExecution, tc.mask, false, false)
			if err == nil {
				t.Errorf("MergeWorkUnitExecution() succeeded, want error")
			}
		})
	}
}

func TestMergeWorkUnitExecution_MessageUpdate(t *testing.T) {
	ctx := context.Background()
	currentTime := time.Now()
	currentMsgTime := currentTime.Add(-time.Hour)
	newMsgTime := currentTime.Add(-time.Minute)

	testCases := []struct {
		name                  string
		currentMsg            *pb.StructuredMessage
		newMsg                *pb.StructuredMessage
		forceUpdate           bool
		deferToExisting       bool
		wantMsgUserFacingText string
	}{
		{
			name: "higher severity",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-error",
				Severity:       pb.StructuredMessage_ERROR,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       false,
			wantMsgUserFacingText: "new-error",
		},
		{
			name: "lower severity",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-error",
				Severity:       pb.StructuredMessage_ERROR,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       false,
			wantMsgUserFacingText: "current-error",
		},
		{
			name: "lower severity forced",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-error",
				Severity:       pb.StructuredMessage_ERROR,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-forced",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           true,
			deferToExisting:       false,
			wantMsgUserFacingText: "new-info-forced",
		},
		{
			name: "same severity newer time",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-newer",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       false,
			wantMsgUserFacingText: "new-info-newer",
		},
		{
			name: "same severity older time",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-older",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       false,
			wantMsgUserFacingText: "current-info",
		},
		{
			name: "same severity same time",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-same-time",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       false,
			wantMsgUserFacingText: "new-info-same-time",
		},
		{
			name: "same severity newer time defer to existing",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-newer",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       true,
			wantMsgUserFacingText: "current-info",
		},
		{
			name: "same severity older time defer to existing",
			currentMsg: &pb.StructuredMessage{
				UserFacingText: "current-info",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(newMsgTime),
			},
			newMsg: &pb.StructuredMessage{
				UserFacingText: "new-info-older",
				Severity:       pb.StructuredMessage_INFO,
				CreateTime:     timestamppb.New(currentMsgTime),
			},
			forceUpdate:           false,
			deferToExisting:       true,
			wantMsgUserFacingText: "current-info",
		},
	}

	messageFields := []struct {
		fieldName string
		maskPath  string
	}{
		{
			fieldName: "SystemMessage",
			maskPath:  "status.system_message",
		},
		{
			fieldName: "UserMessage",
			maskPath:  "status.user_message",
		},
	}

	for _, field := range messageFields {
		t.Run(field.fieldName, func(t *testing.T) {
			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					current := &pb.WorkUnitExecution{
						Name: "experiments/1/workUnits/1/executions/1",
						Status: &pb.WorkUnitStatus{
							State: pb.WorkUnitStatus_RUNNING,
						},
					}
					updated := &pb.WorkUnitExecution{
						Status: &pb.WorkUnitStatus{},
					}

					mask := &fmpb.FieldMask{Paths: []string{field.maskPath}}

					// Set the appropriate message field based on the current field being tested
					switch field.maskPath {
					case "status.system_message":
						current.Status.SystemMessage = tc.currentMsg
						updated.Status.SystemMessage = tc.newMsg
					case "status.user_message":
						current.Status.UserMessage = tc.currentMsg
						updated.Status.UserMessage = tc.newMsg
					}

					got, err := MergeWorkUnitExecution(ctx, current, updated, mask, tc.forceUpdate, tc.deferToExisting)
					if err != nil {
						t.Fatalf("MergeWorkUnitExecution() for %s failed: %v", field.fieldName, err)
					}

					// Get the resulting message field based on the current field being tested
					var resultingMsg *pb.StructuredMessage
					switch field.maskPath {
					case "status.system_message":
						resultingMsg = got.Status.GetSystemMessage()
					case "status.user_message":
						resultingMsg = got.Status.GetUserMessage()
					}

					if resultingMsg.GetUserFacingText() != tc.wantMsgUserFacingText {
						t.Errorf("MergeWorkUnitExecution() for %s returned unexpected message text: got %q, want %q",
							field.fieldName, resultingMsg.GetUserFacingText(), tc.wantMsgUserFacingText)
					}
				})
			}
		})
	}
}
