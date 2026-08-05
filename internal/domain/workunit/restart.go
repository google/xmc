// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workunit

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// RestartExecutionResult holds the computed objects resulting from restarting a single WorkUnitExecution.
type RestartExecutionResult struct {
	ArchivedExecution *essmsgpb.WorkUnitExecution
	NewExecution      *essmsgpb.WorkUnitExecution
	NewEID            int64
	StateDelta        *state.WorkUnitStateDelta
}

// RestartExecution computes the archived work unit execution, the newly created execution in RESTARTING state,
// and the WorkUnitStateDelta for updating denormalized experiment state.
func RestartExecution(
	xid int64,
	wid int64,
	currentExecution *essmsgpb.WorkUnitExecution,
	requester string,
	now time.Time,
) (*RestartExecutionResult, error) {
	if currentExecution == nil {
		return nil, fmt.Errorf("current execution is nil for work unit %d: %w", wid, status.ErrInternal)
	}

	// 1. Get the EID so we can increment it.
	_, _, eid, err := validator.ValidateWorkUnitExecutionName(currentExecution.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit execution name %q: %v: %w", currentExecution.GetName(), err, status.ErrInternal)
	}
	if eid == 0 {
		return nil, fmt.Errorf("invalid eid 0 from work unit execution name %q: %w", currentExecution.GetName(), status.ErrInternal)
	}

	// 2. Set the current execution state to INACTIVE on a cloned message to prevent in-place mutation.
	archivedExecution := proto.Clone(currentExecution).(*essmsgpb.WorkUnitExecution)
	archivedExecution.State = essmsgpb.WorkUnitExecution_INACTIVE
	archivedExecution.UpdateTime = timestamppb.New(now)

	// 3. Create a new execution with RESTARTING status.
	newEID := eid + 1
	newExecution := &essmsgpb.WorkUnitExecution{
		Name: fmt.Sprintf("experiments/%v/workUnits/%v/executions/%v", xid, wid, newEID),
		Status: &essmsgpb.WorkUnitStatus{
			State: essmsgpb.WorkUnitStatus_RESTARTING,
			SystemMessage: &essmsgpb.StructuredMessage{
				UserFacingText: fmt.Sprintf("Work unit is being restarted by %s", requester),
				Severity:       essmsgpb.StructuredMessage_INFO,
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
				Parent:         fmt.Sprintf("experiments/%v/workUnits/%v", xid, wid),
				CreateTime:     timestamppb.New(now),
			},
			UpdateTime:      timestamppb.New(now),
			StateUpdateTime: timestamppb.New(now),
		},
		Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
		State:      essmsgpb.WorkUnitExecution_ACTIVE,
		CreateTime: timestamppb.New(now),
		UpdateTime: timestamppb.New(now),
	}

	// 4. Compute the state delta for experiment denormalization.
	var currentState *essmsgpb.WorkUnitStatus_WorkUnitState
	if status := archivedExecution.GetStatus(); status != nil {
		currentState = status.GetState().Enum()
	}

	stateDelta := &state.WorkUnitStateDelta{
		CurrentState:   currentState,
		NewState:       newExecution.GetStatus().GetState().Enum(),
		CurrentMessage: archivedExecution.GetStatus().GetSystemMessage(),
		NewMessage:     newExecution.GetStatus().GetSystemMessage(),
	}

	return &RestartExecutionResult{
		ArchivedExecution: archivedExecution,
		NewExecution:      newExecution,
		NewEID:            newEID,
		StateDelta:        stateDelta,
	}, nil
}
