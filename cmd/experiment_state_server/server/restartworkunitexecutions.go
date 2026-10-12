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

package server

import (
	"context"
	"fmt"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// restartWorkUnitExecutions archives the active work unit execution for a set of work units, and
// generates a new current work unit execution in the RESTARTING state. It returns a map of
// work unit IDs to the newly created work unit executions.
func restartWorkUnitExecutions(ctx context.Context, txRunner storage.TransactionRunner, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Restarting work unit executions", "wids", wids)

	var newExecutions map[int64]*essmsgpb.WorkUnitExecution
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		newExecutions = make(map[int64]*essmsgpb.WorkUnitExecution, len(wids))
		executionDeltas := make([]*state.WorkUnitStateDelta, 0, len(wids))
		now := time.Now()

		for _, wid := range wids {
			newExecution, delta, err := restartSingleWorkUnitExecution(ctx, tx, requester, xid, wid, now)
			if err != nil {
				return err
			}
			newExecutions[wid] = newExecution
			executionDeltas = append(executionDeltas, delta)
		}

		if err := updateRestartExperimentStatus(ctx, tx, xid, executionDeltas, now); err != nil {
			return err
		}
		logging.Debug(ctx, "Updated experiment status with restarted execution counts", "restartedCount", len(newExecutions))
		return nil
	})
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Work unit execution restarted successfully", "newExecutions", newExecutions)
	return newExecutions, nil
}

// restartSingleWorkUnitExecution archives the current execution of a single work unit, stages a new
// RESTARTING execution, records status and history mutations, and computes the state delta.
func restartSingleWorkUnitExecution(
	ctx context.Context,
	tx *storage.Transaction,
	requester string,
	xid, wid int64,
	now time.Time,
) (*essmsgpb.WorkUnitExecution, *state.WorkUnitStateDelta, error) {
	logging.Debug(ctx, "Processing work unit restart", "wid", wid)

	// 1. Read the WorkUnit row.
	wu, err := tx.WorkUnits.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch work unit: %w", err)
	}

	// 2. Set the execution state to INACTIVE.
	currentExecution := wu.GetCurrentWorkUnitExecution()
	if currentExecution == nil {
		return nil, nil, fmt.Errorf("work unit %d has no current execution: %w", wid, grpcerr.ErrInternal)
	}
	currentExecution.State = essmsgpb.WorkUnitExecution_INACTIVE
	currentExecution.UpdateTime = timestamppb.New(now)

	// 3. Archive the current execution.
	if err := tx.WorkUnits.SaveWorkUnitExecution(ctx, currentExecution); err != nil {
		return nil, nil, fmt.Errorf("failed to archive work unit execution: %w", err)
	}

	// 4. Get the EID so we can increment it.
	_, _, eid, err := validator.ValidateWorkUnitExecutionName(currentExecution.GetName())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate work unit execution name %q: %w: %w", currentExecution.GetName(), grpcerr.ErrInternal, err)
	}
	if eid == 0 {
		return nil, nil, fmt.Errorf("invalid eid 0 from work unit execution name %q: %w", currentExecution.GetName(), grpcerr.ErrInternal)
	}

	// 4.1. Create a new execution with the given state.
	newEID := eid + 1
	newExecution := newRestartingWorkUnitExecution(xid, wid, newEID, requester, now)
	logging.Debug(ctx, "Archiving active execution and generating new execution", "wid", wid, "prevEID", eid, "newEID", newEID)

	// 4.2. Record status message and history mutations.
	if err := recordRestartExecutionHistory(ctx, tx, xid, wid, newEID, wu.GetExecutable(), currentExecution, newExecution); err != nil {
		return nil, nil, err
	}

	// 5. Write the new execution to storage.
	if err := tx.WorkUnits.UpdateCurrentWorkUnitExecution(ctx, xid, wid, newEID, newExecution); err != nil {
		return nil, nil, fmt.Errorf("failed to update current work unit execution: %w", err)
	}

	delta := &state.WorkUnitStateDelta{
		CurrentState:   currentExecution.GetStatus().GetState().Enum(),
		NewState:       newExecution.GetStatus().GetState().Enum(),
		CurrentMessage: currentExecution.GetStatus().GetSystemMessage(),
		NewMessage:     newExecution.GetStatus().GetSystemMessage(),
	}

	return newExecution, delta, nil
}

func newRestartingWorkUnitExecution(xid, wid, newEID int64, requester string, now time.Time) *essmsgpb.WorkUnitExecution {
	nowProto := timestamppb.New(now)
	return &essmsgpb.WorkUnitExecution{
		Name: fmt.Sprintf("experiments/%v/workUnits/%v/executions/%v", xid, wid, newEID),
		Status: &essmsgpb.WorkUnitStatus{
			State: essmsgpb.WorkUnitStatus_RESTARTING,
			SystemMessage: &essmsgpb.StructuredMessage{
				UserFacingText: fmt.Sprintf("Work unit is being restarted by %s", requester),
				Severity:       essmsgpb.StructuredMessage_INFO,
				Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
				Parent:         fmt.Sprintf("experiments/%v/workUnits/%v", xid, wid),
			},
			UpdateTime:      nowProto,
			StateUpdateTime: nowProto,
		},
		Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
		State:      essmsgpb.WorkUnitExecution_ACTIVE,
		CreateTime: nowProto,
		UpdateTime: nowProto,
	}
}

func recordRestartExecutionHistory(
	ctx context.Context,
	tx *storage.Transaction,
	xid, wid, newEID int64,
	executable *essmsgpb.Executable,
	currentExecution, newExecution *essmsgpb.WorkUnitExecution,
) error {
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &wid, &newEID, currentExecution.GetStatus().GetSystemMessage(), newExecution.GetStatus().GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save status message history: %w", err)
	}

	qName := executable.GetSchedulingConstraints().GetQueueName()
	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(executable)
	resMap := acc.IntTotals()
	prevState := currentExecution.GetStatus().GetState().Enum()
	newState := newExecution.GetStatus().GetState().Enum()
	var stateTime time.Time
	if newExecution.GetStatus().GetUpdateTime() != nil {
		stateTime = newExecution.GetStatus().GetUpdateTime().AsTime()
	}
	if err := tx.StatusHistory.SaveWorkUnitStatusHistoryIfChanged(ctx, xid, wid, &newEID, qName, prevState, newState, resMap, stateTime); err != nil {
		return fmt.Errorf("failed to save work unit status history: %w", err)
	}
	return nil
}

// updateRestartExperimentStatus denormalizes execution state deltas and updates the experiment status in storage.
func updateRestartExperimentStatus(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	executionDeltas []*state.WorkUnitStateDelta,
	now time.Time,
) error {
	// 6. Read the Experiment row.
	exp, err := tx.Experiments.FetchExperiment(ctx, xid)
	if err != nil {
		return fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return fmt.Errorf("experiment %d not found", xid)
	}
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	// 7. Update the denormalized status fields in the Experiment row.
	exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
	if err != nil {
		return fmt.Errorf("failed to update experiment status: %w", err)
	}

	// 8. Update the Experiment row.
	exp.UpdateTime = timestamppb.New(now)
	exp.Status.LastChildStatusUpdateTime = timestamppb.New(now)
	exp.Status.UpdateTime = timestamppb.New(now)
	if err := tx.Experiments.UpdateExperimentStatus(ctx, exp); err != nil {
		return fmt.Errorf("failed to update experiment status: %w", err)
	}
	return nil
}
