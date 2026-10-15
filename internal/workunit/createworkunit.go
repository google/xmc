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

	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	experimentdomain "xmanager_cloud/internal/domain/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// CreateWorkUnit creates a new work unit in storage.
func CreateWorkUnit(ctx context.Context, txRunner storage.TransactionRunner, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	exp := &essmsgpb.Experiment{}
	var committedWU *essmsgpb.WorkUnit
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		now := time.Now()
		localWU := proto.Clone(wu).(*essmsgpb.WorkUnit)

		// 1. Extract the XID and WID from the work unit name.
		xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(localWU.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit name: %w (%v)", grpcerr.ErrInvalidArgument, err)
		}

		ensureWorkUnitExecutionStatus(localWU)

		// 1.1. If the work unit ID is 0, fetch the next available ID and update the names.
		initialEID := int64(1)
		if wid == 0 {
			wid, err = tx.WorkUnits.FetchNextWorkUnitID(ctx, xid)
			if err != nil {
				return fmt.Errorf("failed to fetch next work unit ID: %w", err)
			}
			localWU.Name = validator.ConstructWorkUnitName(xid, wid)
			localWU.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialEID)
			setParentIfNonNil(localWU.CurrentWorkUnitExecution.Status.SystemMessage, localWU.GetName())
		}

		// 1.2. Set the initial timestamps to the current time.
		localWU.CreateTime = timestamppb.New(now)
		localWU.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.CreateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.Status.UpdateTime = timestamppb.New(now)
		localWU.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now)

		// 2. Insert the work unit into storage.
		if err := tx.WorkUnits.InsertWorkUnit(ctx, localWU); err != nil {
			return fmt.Errorf("failed to insert work unit: %w", err)
		}

		// 3. Read the Experiment row.
		exp, err = tx.Experiments.FetchExperiment(ctx, xid)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		// 4. Update the denormalized status fields and resources in the Experiment row using domain rules.
		if err := recordWorkUnitCreationHistory(ctx, tx, xid, wid, initialEID, localWU); err != nil {
			return err
		}

		creationRes, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, localWU, now)
		if err != nil {
			return fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
		}
		exp = creationRes.Experiment
		resourcesChanged := creationRes.ResourcesChanged

		// 5. Buffer mutations for Experiment status and resources via thin repository methods.
		// TODO(b/517479143): Consolidate UpdateExperimentStatus and UpdateExperimentResources into a single mutation write to prevent duplicate row updates for the same Experiments row in Spanner.
		if err := tx.Experiments.UpdateExperimentStatus(ctx, exp); err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}
		if resourcesChanged {
			if err := tx.Experiments.UpdateExperimentResources(ctx, exp); err != nil {
				return fmt.Errorf("failed to update experiment resources: %w", err)
			}
		}

		committedWU = localWU
		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	// Populate timestamps back onto input proto pointer for caller convenience (matching CreateExperiment behavior).
	wu.CreateTime = committedWU.CreateTime
	wu.UpdateTime = committedWU.UpdateTime
	if wu.CurrentWorkUnitExecution != nil {
		wu.CurrentWorkUnitExecution.CreateTime = committedWU.CurrentWorkUnitExecution.CreateTime
		wu.CurrentWorkUnitExecution.UpdateTime = committedWU.CurrentWorkUnitExecution.UpdateTime
		if wu.CurrentWorkUnitExecution.Status != nil {
			wu.CurrentWorkUnitExecution.Status.UpdateTime = committedWU.CurrentWorkUnitExecution.Status.UpdateTime
			wu.CurrentWorkUnitExecution.Status.StateUpdateTime = committedWU.CurrentWorkUnitExecution.Status.StateUpdateTime
		}
	}

	logging.Info(ctx, "Written work unit to storage", "work_unit", committedWU)
	logging.Info(ctx, "Updated experiment successfully", "experiment", exp)

	return committedWU, nil
}

func ensureWorkUnitExecutionStatus(wu *essmsgpb.WorkUnit) {
	if wu.CurrentWorkUnitExecution == nil {
		wu.CurrentWorkUnitExecution = &essmsgpb.WorkUnitExecution{}
	}
	if wu.CurrentWorkUnitExecution.Status == nil {
		wu.CurrentWorkUnitExecution.Status = &essmsgpb.WorkUnitStatus{}
	}
}

func recordWorkUnitCreationHistory(ctx context.Context, tx *storage.Transaction, xid, wid, initialEID int64, localWU *essmsgpb.WorkUnit) error {
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &wid, &initialEID, nil, localWU.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save system status message history: %w", err)
	}

	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(localWU.GetExecutable())
	resMap := acc.IntTotals()
	qName := localWU.GetExecutable().GetSchedulingConstraints().GetQueueName()
	var newState *essmsgpb.WorkUnitStatus_WorkUnitState
	var stateTime time.Time
	if status := localWU.GetCurrentWorkUnitExecution().GetStatus(); status != nil {
		newState = status.GetState().Enum()
		if status.GetUpdateTime() != nil {
			stateTime = status.GetUpdateTime().AsTime()
		}
	}
	if err := tx.StatusHistory.SaveWorkUnitStatusHistoryIfChanged(ctx, xid, wid, &initialEID, qName, nil, newState, resMap, stateTime); err != nil {
		return fmt.Errorf("failed to save work unit status history: %w", err)
	}
	return nil
}
