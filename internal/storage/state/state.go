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
// Package state provides utilities for updating the state of experiments and work unit executions.
package state

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

var (
	// WorkUnitAllowedForwardTransitions is a map of states that are allowed to transition to other
	// states. The key is the current state, and the value is the list of states that are allowed to
	// transition to.
	// A missing key means that all transitions are allowed.
	WorkUnitAllowedForwardTransitions = map[essmsgpb.WorkUnitStatus_WorkUnitState][]essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_STOPPED:   {},
		essmsgpb.WorkUnitStatus_COMPLETED: {},
		essmsgpb.WorkUnitStatus_FAILED:    {},
		essmsgpb.WorkUnitStatus_STOPPING: {
			essmsgpb.WorkUnitStatus_STOPPED,
		},
	}

	// WorkUnitAllowedBackwardTransitions is a map of states that are allowed to transition from other
	// states. The key is the current state, and the value is the list of states that are allowed to
	// transition to.
	// A missing key means that all transitions are allowed.
	WorkUnitAllowedBackwardTransitions = map[essmsgpb.WorkUnitStatus_WorkUnitState][]essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_INITIALIZING: {},
		essmsgpb.WorkUnitStatus_RESTARTING:   {},
	}

	// WorkUnitTerminalStates are the terminal states for a work unit.
	WorkUnitTerminalStates = []essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_STOPPED,
		essmsgpb.WorkUnitStatus_COMPLETED,
		essmsgpb.WorkUnitStatus_FAILED,
	}

	// WorkUnitTimeIgnoredStates are the states that should ignore the state update time. For example,
	// if a work unit is transitioning to STOPPING, but the actuator set the state to RUNNING in the
	// meantime, we should still allow the state to transition to STOPPED.
	// This doesn't override any other checks, like the terminal state checks. It just means that
	// updates to these states will be accepted even if the state update time is not more recent than
	// the current state update time.
	WorkUnitTimeIgnoredStates = []essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_STOPPING,
	}

	// WorkUnitStatusRetainingStates are the states that do not trigger a status message reset when
	// transitioning. The logic for each transition is:
	// 1. * -> FAILED: Almost certainly, the last valid message is the one that caused the failure.
	// 2. STOPPING -> STOPPED: A transition into STOPPING from a non-terminal state would have
	//    already reset the message. Any message present in the STOPPING state is likely
	//    relevant to the subsequent STOPPED state and should be preserved.
	// We have to exclude COMPLETED because when a work unit completes successfully, clearly, whatever
	// issue there was, it's no longer relevant. If a FATAL message was present, it should still be
	// present through a different control flow.
	WorkUnitStatusRetainingStates = []essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_STOPPED,
		essmsgpb.WorkUnitStatus_FAILED,
	}
)

// WorkUnitStateDelta represents the delta between the current and new state of a work unit
// execution.
type WorkUnitStateDelta struct {
	CurrentState   *essmsgpb.WorkUnitStatus_WorkUnitState
	NewState       *essmsgpb.WorkUnitStatus_WorkUnitState
	CurrentMessage *essmsgpb.StructuredMessage
	NewMessage     *essmsgpb.StructuredMessage
}

// DenormalizeWorkUnitStatus updates the status of an experiment based on the status of its work
// units.
func DenormalizeWorkUnitStatus(ctx context.Context, exp *essmsgpb.Experiment, workUnitStateDeltas []*WorkUnitStateDelta) (*essmsgpb.Experiment, error) {
	// 1. Convert the child state counts to a map for easier lookup.
	stateCountsMap := make(map[essmsgpb.WorkUnitStatus_WorkUnitState]int64)
	for _, stateCount := range exp.Status.ChildStateCounts {
		stateCountsMap[stateCount.GetState()] = stateCount.GetCount()
	}
	severityCountsMap := make(map[essmsgpb.StructuredMessage_Severity]int64)
	for _, severityCount := range exp.Status.MessageSeverityCounts {
		severityCountsMap[severityCount.GetSeverity()] = severityCount.GetCount()
	}

	// 2. Update the state counts map based on the deltas.
	for _, delta := range workUnitStateDeltas {
		if delta.CurrentState != nil {
			if val, ok := stateCountsMap[*delta.CurrentState]; ok {
				stateCountsMap[*delta.CurrentState] = val - 1
			} else {
				logging.Error(ctx, "Current state not found in state counts map", "current_state", *delta.CurrentState, "state_counts_map", stateCountsMap)
			}
		}
		if delta.NewState != nil {
			if val, ok := stateCountsMap[*delta.NewState]; ok {
				stateCountsMap[*delta.NewState] = val + 1
			} else {
				stateCountsMap[*delta.NewState] = 1
			}
		}
		currentSeverity := essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED
		if delta.CurrentMessage != nil {
			currentSeverity = delta.CurrentMessage.Severity
		}
		newSeverity := essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED
		if delta.NewMessage != nil {
			newSeverity = delta.NewMessage.Severity
		}

		updateSeverityCounts(ctx, severityCountsMap, currentSeverity, newSeverity)
	}

	// 3. Convert the map back to a list.
	var newChildStateCounts []*essmsgpb.ExperimentStatus_StateCount
	for state, count := range stateCountsMap {
		if count > 0 {
			newChildStateCounts = append(newChildStateCounts, &essmsgpb.ExperimentStatus_StateCount{State: state, Count: count})
		}
	}
	exp.Status.ChildStateCounts = newChildStateCounts

	// 4. Convert severity map back to list.
	var newMessageSeverityCounts []*essmsgpb.ExperimentStatus_MessageSeverityCount
	for severity, count := range severityCountsMap {
		if count > 0 {
			newMessageSeverityCounts = append(newMessageSeverityCounts, &essmsgpb.ExperimentStatus_MessageSeverityCount{Severity: severity, Count: count})
		}
	}
	exp.Status.MessageSeverityCounts = newMessageSeverityCounts

	// 5. Update the active state of the experiment.
	return UpdateExperimentActiveState(exp)
}

func updateSeverityCounts(ctx context.Context, severityCountsMap map[essmsgpb.StructuredMessage_Severity]int64, currentSeverity, newSeverity essmsgpb.StructuredMessage_Severity) {
	if currentSeverity == newSeverity {
		return
	}
	if currentSeverity != essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED {
		if val, ok := severityCountsMap[currentSeverity]; ok {
			severityCountsMap[currentSeverity] = val - 1
		} else {
			logging.Error(ctx, "Current severity not found in severity counts map", "current_severity", currentSeverity, "severity_counts_map", severityCountsMap)
		}
	}
	if newSeverity != essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED {
		severityCountsMap[newSeverity] = severityCountsMap[newSeverity] + 1
	}
}

// UpdateExperimentActiveState updates the active state of an experiment based on the launch state
// and the active states of its work units.
func UpdateExperimentActiveState(exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	if exp.GetStatus().GetLaunchState() == essmsgpb.ExperimentStatus_FAILED {
		// If the experiment failed to launch, it is considered inactive.
		exp.Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
	} else if exp.GetStatus().GetLaunchState() == essmsgpb.ExperimentStatus_LAUNCHING {
		// If the experiment is launching, it is considered active.
		exp.Status.ActiveState = essmsgpb.ExperimentStatus_ACTIVE
	} else {
		// Otherwise, the experiment is considered active if any of its work units are active.
		exp.Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
		for _, stateCount := range exp.GetStatus().GetChildStateCounts() {
			if !slices.Contains(WorkUnitTerminalStates, stateCount.GetState()) {
				exp.Status.ActiveState = essmsgpb.ExperimentStatus_ACTIVE
				return exp, nil
			}
		}
	}

	return exp, nil
}

// FindBestStatusMessage selects the best status message from a list of candidates. The best message
// is one with the longest stack_trace or the one with the longest message; heuristically, this is
// typically the most informative message). If there are multiple messages with the same length, we
// return the first one in the list.
func FindBestStatusMessage(candidates []*essmsgpb.StructuredMessage) *essmsgpb.StructuredMessage {
	if len(candidates) == 0 {
		return nil
	}
	bestMsg := slices.MaxFunc(candidates, func(a, b *essmsgpb.StructuredMessage) int {
		if diff := len(a.GetAuxiliaryInfo().GetStackTrace()) - len(b.GetAuxiliaryInfo().GetStackTrace()); diff != 0 {
			return diff
		}
		return len(a.GetUserFacingText()) - len(b.GetUserFacingText())
	})
	return bestMsg
}

// ShouldUpdateWorkUnitExecutionState returns whether the new state of a work unit execution
// should be written to storage.
func ShouldUpdateWorkUnitExecutionState(ctx context.Context, currentExecution *essmsgpb.WorkUnitExecution, newExecution *essmsgpb.WorkUnitExecution) (bool, error) {
	currentState := currentExecution.GetStatus().GetState()
	newState := newExecution.GetStatus().GetState()

	// If the state is unchanged, we should not update the state.
	if currentState == newState {
		logging.Info(ctx, "State is unchanged", "current_state", currentState, "new_state", newState)
		return false, nil
	}

	currentXID, currentWID, currentEID, err := validator.ValidateWorkUnitExecutionName(currentExecution.GetName())
	if err != nil {
		return false, fmt.Errorf("failed to validate current work unit execution name: %v", err)
	}

	newXID, newWID, newEID, err := validator.ValidateWorkUnitExecutionName(newExecution.GetName())
	if err != nil {
		return false, fmt.Errorf("failed to validate new work unit execution name: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}

	// If the work unit execution XID or WID doesn't match, something is very wrong. Reject the
	// update.
	if currentXID != newXID || currentWID != newWID {
		logging.Error(ctx, "Received state update for two different work units", "current_execution_name", currentExecution.GetName(), "new_execution_name", newExecution.GetName())
		return false, nil
	}

	// If the execution ID doesn't match, likely we have restarted the work unit execution and should
	// not update the state.
	if currentEID != newEID {
		logging.Warn(ctx, "Execution ID is different", "current_eid", currentEID, "new_eid", newEID)
		return false, nil
	}

	allowedForwardTransitions, ok := WorkUnitAllowedForwardTransitions[currentState]
	if ok {
		if !slices.Contains(allowedForwardTransitions, newState) {
			logging.Warn(ctx, "State is not allowed to transition", "current_state", currentState, "new_state", newState)
			return false, nil
		}
	}

	allowedBackwardTransitions, ok := WorkUnitAllowedBackwardTransitions[newState]
	if ok {
		if !slices.Contains(allowedBackwardTransitions, currentState) {
			logging.Warn(ctx, "State is not allowed to transition backward", "new_state", newState, "current_state", currentState)
			return false, nil
		}
	}

	// If the state update time is zero, we should not update the state. This is to prevent the state
	// update time from being overwritten with a zero value because the caller didn't set it.
	// Non-state updates will never get here since it will short-circuit above.
	if newExecution.GetStatus().GetStateUpdateTime().AsTime().IsZero() {
		logging.Warn(ctx, "State update time is zero", "state_update_time", newExecution.GetStatus().GetStateUpdateTime())
		return false, nil
	}

	// If the state update time is not more recent than the current state update time, we
	// should not update the state. This is to prevent out-of-order updates from overwriting
	// more recent state updates. If both updates happen at exactly the same time, we write them
	// in the order they are received.
	if !slices.Contains(WorkUnitTimeIgnoredStates, newState) && currentExecution.GetStatus().GetStateUpdateTime().AsTime().After(newExecution.GetStatus().GetStateUpdateTime().AsTime()) {
		logging.Warn(ctx, "State update time is not more recent than the current state update time", "new_state_update_time", newExecution.GetStatus().GetStateUpdateTime(), "current_state_update_time", currentExecution.GetStatus().GetStateUpdateTime())
		return false, nil
	}

	// Otherwise, the update is valid.
	return true, nil
}

// ShouldStatusMessageSupercede returns whether the new status message should supercede the current
// status message. This is used to determine whether the status message should be updated.
func ShouldStatusMessageSupercede(ctx context.Context, currentMsg *essmsgpb.StructuredMessage, newMsg *essmsgpb.StructuredMessage) bool {
	// If there is no current message, and there is a new message, we should update.
	if currentMsg == nil && newMsg != nil {
		return true
	}

	// If there is no new message, we should not update.
	if newMsg == nil {
		return false
	}

	// If the incoming message's parent does not match the current message's parent, we should not
	// update the status message.
	if currentMsg.GetParent() != newMsg.GetParent() {
		logging.Warn(ctx, "Incoming message parent does not match the current message parent", "new_msg_parent", newMsg.GetParent(), "current_msg_parent", currentMsg.GetParent())
		return false
	}

	// If the incoming message's create time is zero, we should not update the status message. This is to
	// prevent the update time from being overwritten with a zero value because the caller didn't set
	// it.
	if newMsg.GetCreateTime().AsTime().IsZero() {
		logging.Warn(ctx, "Incoming message create time is zero", "create_time", newMsg.GetCreateTime())
		return false
	}

	// If the severity of the incoming message is lower than the current message, we should not update
	// the status message.
	if currentMsg.GetSeverity() > newMsg.GetSeverity() {
		logging.Warn(ctx, "Incoming message severity is lower than the current message severity", "new_msg_severity", newMsg.GetSeverity(), "current_msg_severity", currentMsg.GetSeverity())
		return false
	}

	// If the severity of the incoming message is higher than the current message, we should update
	// the status message.
	if currentMsg.GetSeverity() < newMsg.GetSeverity() {
		logging.Info(ctx, "Incoming message severity is higher than the current message severity", "new_msg_severity", newMsg.GetSeverity(), "current_msg_severity", currentMsg.GetSeverity())
		return true
	}

	// If the incoming messages create time is not more recent than the current message's update
	// time, we should not update the status message. This is to prevent out-of-order updates from
	// overwriting more recent status message updates. If both updates happen at exactly the same
	// time, we write them in the order they are received.
	if currentMsg.GetCreateTime().AsTime().After(newMsg.GetCreateTime().AsTime()) {
		logging.Warn(ctx, "Incoming message update time is not more recent than the current message update time", "new_msg_create_time", newMsg.GetCreateTime(), "current_msg_create_time", currentMsg.GetCreateTime())
		return false
	}

	return true
}

// GetBestMessageFromStatus returns the most relevant status message from work unit status.
// The message is the one with the highest severity between the system and user messages.
// If severities are equal, FindBestStatusMessage is used to determine the best message.
func GetBestMessageFromStatus(status *essmsgpb.WorkUnitStatus) *essmsgpb.StructuredMessage {
	sys := status.GetSystemMessage()
	usr := status.GetUserMessage()
	if sys == nil {
		return usr
	}
	if usr == nil {
		return sys
	}
	if sys.GetSeverity() > usr.GetSeverity() {
		return sys
	}
	if usr.GetSeverity() > sys.GetSeverity() {
		return usr
	}
	return FindBestStatusMessage([]*essmsgpb.StructuredMessage{sys, usr})
}

// ShouldResetStatusMessage returns whether the status message should be reset.
// A message reset is warranted if the work unit's state has changed in a way that makes a previous
// status message irrelevant.
//
// The message is NOT reset if:
// - The state is unchanged.
// - The update request includes a new status message.
// - The existing message is nil or has FATAL severity.
// - The new state is one that should preserve the message (e.g., FAILED, STOPPED).
//
// Otherwise, the message should be reset.
func ShouldResetStatusMessage(ctx context.Context, currentExecution *essmsgpb.WorkUnitExecution, newExecution *essmsgpb.WorkUnitExecution, currentMsg, newMsg *essmsgpb.StructuredMessage) (bool, error) {
	// If the state is unchanged, we should not reset the status message.
	if currentExecution.GetStatus().GetState() == newExecution.GetStatus().GetState() {
		return false, nil
	}

	// If the new execution changed the message, we should not reset the status message, as the update
	// must have contained both a state update and a message update.
	if !proto.Equal(currentMsg, newMsg) {
		return false, nil
	}

	// If there is no current message, or if the current message is FATAL, we should not reset
	// the status message.
	if currentMsg == nil || currentMsg.GetSeverity() == essmsgpb.StructuredMessage_FATAL {
		return false, nil
	}

	// If the caller transitions to a state that retains the status message, we should not reset it.
	if slices.Contains(WorkUnitStatusRetainingStates, newExecution.GetStatus().GetState()) {
		return false, nil
	}

	return true, nil
}

// ShouldResetSchedulingInfo returns whether the scheduling info should be reset.
// The scheduling info is reset if the work unit's state is not SCHEDULING.
func ShouldResetSchedulingInfo(ctx context.Context, currentExecution *essmsgpb.WorkUnitExecution, newExecution *essmsgpb.WorkUnitExecution) bool {
	if currentExecution.GetStatus().GetState() == newExecution.GetStatus().GetState() {
		return false
	}
	if newExecution.GetStatus().GetState() == essmsgpb.WorkUnitStatus_SCHEDULING {
		return false
	}
	return true
}
