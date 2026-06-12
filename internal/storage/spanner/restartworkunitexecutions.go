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
package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// RestartWorkUnitExecutions archives the active work unit execution for a set of work units, and
// generates a new current work unit execution in the RESTARTING state. It returns a map of
// work unit IDs to the newly created work unit executions.
func (s *Storage) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Restarting work unit executions", "wids", wids)

	var newExecutions map[int64]*essmsgpb.WorkUnitExecution
	var executionDeltas []*state.WorkUnitStateDelta
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		newExecutions = make(map[int64]*essmsgpb.WorkUnitExecution)
		executionDeltas = make([]*state.WorkUnitStateDelta, 0)
		now := time.Now()

		for _, wid := range wids {
			// 1. Read the WorkUnit row.
			wu, err := s.fetchWorkUnitTransaction(ctx, xid, wid, tx)
			if err != nil {
				return fmt.Errorf("failed to fetch work unit: %v: %w", err, status.ErrInternal)
			}

			// 2. Set the execution state to INACTIVE.
			currentExecution := wu.GetCurrentWorkUnitExecution()
			currentExecution.State = essmsgpb.WorkUnitExecution_INACTIVE
			currentExecution.UpdateTime = timestamppb.New(now)

			// 3. Archive the current execution.
			archiveMutation, err := spannerdata.WorkUnitExecutionProtoToInsertMutation(currentExecution)
			if err != nil {
				return fmt.Errorf("failed to create mutation for archived work unit execution: %v: %w", err, status.ErrInternal)
			}
			tx.BufferWrite([]*spanner.Mutation{archiveMutation})

			// 4. Get the EID so we can increment it.
			_, _, eid, err := validator.ValidateWorkUnitExecutionName(currentExecution.GetName())
			if err != nil {
				return fmt.Errorf("failed to validate work unit execution name %q: %v: %w", currentExecution.GetName(), err, status.ErrInternal)
			}
			if eid == 0 {
				return fmt.Errorf("invalid eid 0 from work unit execution name %q: %w", currentExecution.GetName(), status.ErrInternal)
			}

			// 4.1. Create a new execution with the given state.
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
					},
					UpdateTime:      timestamppb.New(now),
					StateUpdateTime: timestamppb.New(now),
				},
				Metadata:   &essmsgpb.WorkUnitExecutionMetadata{},
				State:      essmsgpb.WorkUnitExecution_ACTIVE,
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(now),
			}
			newExecutions[wid] = newExecution

			// 4.2. Add the new execution to the execution deltas to update the experiment status.
			executionDeltas = append(executionDeltas, &state.WorkUnitStateDelta{
				CurrentState:   currentExecution.GetStatus().GetState().Enum(),
				NewState:       newExecution.GetStatus().GetState().Enum(),
				CurrentMessage: currentExecution.GetStatus().GetSystemMessage(),
				NewMessage:     newExecution.GetStatus().GetSystemMessage(),
			})

			s.saveStatusMessageHistoryIfChanged(ctx, xid, &wid, &newEID, currentExecution.GetStatus().GetSystemMessage(), newExecution.GetStatus().GetSystemMessage(), tx)

			// 5. Write the new execution to storage.
			tx.BufferWrite([]*spanner.Mutation{
				spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, newEID, newExecution),
			})
		}

		// 6. Read the Experiment row.
		exp, err := s.fetchExperimentTransaction(ctx, xid, tx)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
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
		expMutation, err := spannerdata.ExperimentProtoToStatusUpdateMutation(exp)
		if err != nil {
			logging.Error(ctx, "Failed to create mutation for experiment", "experiment", exp, "error", err)
			return err
		}
		tx.BufferWrite([]*spanner.Mutation{expMutation})

		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to restart work unit executions", "error", err, "wids", wids)
		return nil, err
	}

	logging.Info(ctx, "Work unit execution restarted successfully", "newExecutions", newExecutions)

	return newExecutions, nil
}
