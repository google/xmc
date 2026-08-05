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

package workunit

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	domainwu "xmanager_cloud/internal/domain/workunit"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// RestartWorkUnitExecutions archives the active work unit execution for a set of work units, and
// generates a new current work unit execution in the RESTARTING state. It returns a map of
// work unit IDs to the newly created work unit executions.
func (r *Repository) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Restarting work unit executions", "wids", wids)

	var newExecutions map[int64]*essmsgpb.WorkUnitExecution
	var executionDeltas []*state.WorkUnitStateDelta
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		newExecutions = make(map[int64]*essmsgpb.WorkUnitExecution)
		executionDeltas = make([]*state.WorkUnitStateDelta, 0)
		now := time.Now()

		for _, wid := range wids {
			// 1. Read the WorkUnit row.
			wu, err := r.FetchWorkUnitTransaction(ctx, xid, wid, tx)
			if err != nil {
				return fmt.Errorf("failed to fetch work unit: %v: %w", err, status.ErrInternal)
			}

			// 2. Delegate execution state transition calculation to domain/workunit package.
			result, err := domainwu.RestartExecution(xid, wid, wu.GetCurrentWorkUnitExecution(), requester, now)
			if err != nil {
				return err
			}

			// 3. Archive the current execution.
			archiveMutation, err := spannerdata.WorkUnitExecutionProtoToInsertMutation(result.ArchivedExecution)
			if err != nil {
				return fmt.Errorf("failed to create mutation for archived work unit execution: %v: %w", err, status.ErrInternal)
			}
			tx.BufferWrite([]*spanner.Mutation{archiveMutation})

			newExecutions[wid] = result.NewExecution
			executionDeltas = append(executionDeltas, result.StateDelta)

			statusmessage.SaveStatusMessageHistoryIfChanged(
				ctx,
				r.statusMessageHistoryEnabled,
				xid,
				&wid,
				&result.NewEID,
				result.ArchivedExecution.GetStatus().GetSystemMessage(),
				result.NewExecution.GetStatus().GetSystemMessage(),
				tx,
			)

			// 4. Write the new execution to storage.
			tx.BufferWrite([]*spanner.Mutation{
				spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, result.NewEID, result.NewExecution),
			})
		}

		if r.experimentRepo == nil {
			return fmt.Errorf("experiment repository is not initialized")
		}
		var exp *essmsgpb.Experiment
		var err error
		// 6. Read the Experiment row.
		exp, err = r.experimentRepo.FetchExperimentTransaction(ctx, xid, tx)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		// 7. Update the denormalized status fields in the Experiment row.
		exp, err = state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
		if err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}

		// 8. Update the Experiment row.
		// TODO(b/517479143): Refactor to use r.experimentRepo.UpdateExperimentStatus(ctx, tx, exp) DAO method.
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
