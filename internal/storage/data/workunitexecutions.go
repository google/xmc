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
	"fmt"

	"golang/protobuf/v2/proto/proto"
	fmpb "protobuf/field_mask_go_proto"
	"task/go/status"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/state/state"
)

// MergeWorkUnitExecution merges an updated WorkUnitExecution proto message with a current one using a field mask.
// The forceStatusMessageUpdate and deferToExistingMessage parameters control how the
// status message fields are updated. If forceStatusMessageUpdate is true,
// deferToExistingMessage is ignored and the message is always updated. If
// forceStatusMessageUpdate is false, deferToExistingMessage is used to defer to an existing
// message if it has the same severity as the incoming message.
func MergeWorkUnitExecution(ctx context.Context, currentExecution *pb.WorkUnitExecution, updatedExecution *pb.WorkUnitExecution, mask *fmpb.FieldMask, forceStatusMessageUpdate bool, deferToExistingMessage bool) (*pb.WorkUnitExecution, error) {
	if currentExecution == nil {
		return nil, fmt.Errorf("current execution is nil: %w", status.ErrInvalidArgument)
	}
	if updatedExecution == nil {
		return nil, fmt.Errorf("updated execution is nil: %w", status.ErrInvalidArgument)
	}
	if len(mask.GetPaths()) == 0 {
		return nil, fmt.Errorf("update mask is empty: %w", status.ErrInvalidArgument)
	}

	newExecution := proto.Clone(currentExecution).(*pb.WorkUnitExecution)
	for _, path := range mask.GetPaths() {
		switch path {
		case "metadata":
			newExecution.Metadata = updatedExecution.Metadata
		case "metadata.management_cluster":
			newExecution.Metadata.ManagementCluster = updatedExecution.Metadata.ManagementCluster
		case "metadata.management_cluster.project":
			newExecution.Metadata.ManagementCluster.Project = updatedExecution.Metadata.ManagementCluster.Project
		case "metadata.management_cluster.location":
			newExecution.Metadata.ManagementCluster.Location = updatedExecution.Metadata.ManagementCluster.Location
		case "metadata.management_cluster.cluster_name":
			newExecution.Metadata.ManagementCluster.ClusterName = updatedExecution.Metadata.ManagementCluster.ClusterName
		case "metadata.executing_cluster":
			newExecution.Metadata.ExecutingCluster = updatedExecution.Metadata.ExecutingCluster
		case "metadata.executing_cluster.project":
			newExecution.Metadata.ExecutingCluster.Project = updatedExecution.Metadata.ExecutingCluster.Project
		case "metadata.executing_cluster.location":
			newExecution.Metadata.ExecutingCluster.Location = updatedExecution.Metadata.ExecutingCluster.Location
		case "metadata.executing_cluster.cluster_name":
			newExecution.Metadata.ExecutingCluster.ClusterName = updatedExecution.Metadata.ExecutingCluster.ClusterName
		case "metadata.type":
			newExecution.Metadata.Type = updatedExecution.Metadata.Type
		case "metadata.resource_name":
			newExecution.Metadata.ResourceName = updatedExecution.Metadata.ResourceName
		case "metadata.namespace":
			newExecution.Metadata.Namespace = updatedExecution.Metadata.Namespace
		case "metadata.service_account":
			newExecution.Metadata.ServiceAccount = updatedExecution.Metadata.ServiceAccount
		case "metadata.node_selectors":
			newExecution.Metadata.NodeSelectors = updatedExecution.Metadata.NodeSelectors
		case "metadata.resource_flavor":
			newExecution.Metadata.ResourceFlavor = updatedExecution.Metadata.ResourceFlavor
		case "metadata.multi_kueue_cluster_name":
			newExecution.Metadata.MultiKueueClusterName = updatedExecution.Metadata.MultiKueueClusterName
		case "metadata.mlrun_url":
			newExecution.Metadata.MlrunUrl = updatedExecution.Metadata.MlrunUrl
		case "status":
			newExecution.Status = updatedExecution.Status
		case "status.state":
			if updatedExecution.Status.StateUpdateTime == nil {
				return nil, fmt.Errorf("StateUpdateTime must be set when updating state: %w", status.ErrInvalidArgument)
			}
			newExecution.Status.State = updatedExecution.Status.State

			// This is a special case, because we want the time the caller thinks the state was updated,
			// not the time this API received the update.
			newExecution.Status.StateUpdateTime = updatedExecution.Status.StateUpdateTime
		case "status.system_message":
			newExecution.Status.SystemMessage = updatedExecution.Status.SystemMessage
		case "status.scheduling_information":
			newExecution.Status.SchedulingInformation = updatedExecution.Status.SchedulingInformation
		case "status.user_message":
			newExecution.Status.UserMessage = updatedExecution.Status.UserMessage
		case "status.detailed_status":
			newExecution.Status.DetailedStatus = updatedExecution.Status.DetailedStatus
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}

	// Check if the state should be updated. This is a bit more complicated than a simple proto merge
	// because we need to ensure the state update time is more recent than the current state update
	// time, and that the state is allowed to be updated (e.g. it can't be a terminal state), and
	// other special cases.
	shouldUpdateState, err := state.ShouldUpdateWorkUnitExecutionState(ctx, currentExecution, newExecution)
	if err != nil {
		return nil, err
	}

	// If the state shouldn't be updated, we should set the state to the current state. We greedily
	// update the state above, so we need to reset it here. If the state is unchanged, it's already
	// set correctly.
	if !shouldUpdateState {
		newExecution.Status.State = currentExecution.Status.State
		// Make sure we reset the time if the state change is not allowed.
		newExecution.Status.StateUpdateTime = currentExecution.Status.StateUpdateTime
	}

	// Check if the status message should be updated. We greedily update the status message above, so
	// we need to reset it here if the update is not allowed. If the status message is unchanged, it's
	// already set correctly.
	// If forceStatusMessageUpdate is true, we skip these checks and the message will be updated
	// regardless of the value of deferToExistingMessage.
	if !forceStatusMessageUpdate {
		newExecution.Status.SystemMessage = resolveStatusMessage(ctx, currentExecution.Status.SystemMessage, newExecution.Status.SystemMessage, deferToExistingMessage)
		newExecution.Status.UserMessage = resolveStatusMessage(ctx, currentExecution.Status.UserMessage, newExecution.Status.UserMessage, deferToExistingMessage)
	}

	// Check if the status message should be reset to nil. This is done after the above checks, so
	// that if the caller explicitly wants to update the message, it doesn't get reset.
	shouldResetSystemMsg, err := state.ShouldResetStatusMessage(ctx, currentExecution, newExecution, currentExecution.GetStatus().GetSystemMessage(), newExecution.GetStatus().GetSystemMessage())
	if err != nil {
		return nil, err
	}

	shouldResetUserMsg, err := state.ShouldResetStatusMessage(ctx, currentExecution, newExecution, currentExecution.GetStatus().GetUserMessage(), newExecution.GetStatus().GetUserMessage())
	if err != nil {
		return nil, err
	}

	shouldResetSchedulingInfo := state.ShouldResetSchedulingInfo(ctx, currentExecution, newExecution)

	if shouldResetSchedulingInfo {
		newExecution.Status.SchedulingInformation = nil
	}
	if shouldResetSystemMsg {
		newExecution.Status.SystemMessage = nil
	}
	if shouldResetUserMsg {
		newExecution.Status.UserMessage = nil
	}

	return newExecution, nil
}

func resolveStatusMessage(ctx context.Context, currentMsg, newMsg *pb.StructuredMessage, deferToExistingMessage bool) *pb.StructuredMessage {
	if currentMsg == nil {
		return newMsg
	}
	shouldDefer := deferToExistingMessage && currentMsg.GetSeverity() == newMsg.GetSeverity()
	if !state.ShouldStatusMessageSupercede(ctx, currentMsg, newMsg) || shouldDefer {
		return currentMsg
	}
	return newMsg
}
