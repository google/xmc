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

const initialWorkUnitExecutionID = int64(1)

// CreateWorkUnit validates and persists a new work unit, records initial status and message
// history, and updates parent experiment status and resource totals in a single transaction.
func CreateWorkUnit(ctx context.Context, txRunner storage.TransactionRunner, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	var committedWU *essmsgpb.WorkUnit
	var exp *essmsgpb.Experiment
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		committedWU, exp, err = createWorkUnitInTxn(ctx, tx, wu, time.Now())
		return err
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written work unit to storage", "work_unit", committedWU)
	logging.Info(ctx, "Updated experiment successfully", "experiment", exp)

	return committedWU, nil
}

// CreateWorkUnit creates a new work unit using the Updater's transaction runner.
func (u *Updater) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	return CreateWorkUnit(ctx, u.txRunner, wu)
}

type preparedWorkUnit struct {
	wu  *essmsgpb.WorkUnit
	xid int64
	wid int64
}

func createWorkUnitInTxn(ctx context.Context, tx *storage.Transaction, wu *essmsgpb.WorkUnit, now time.Time) (*essmsgpb.WorkUnit, *essmsgpb.Experiment, error) {
	prepared, err := prepareLocalWorkUnit(ctx, tx.WorkUnits, wu, now)
	if err != nil {
		return nil, nil, err
	}

	// Populate timestamps back onto input proto pointer for caller convenience (matching CreateExperiment behavior).
	writeBackWorkUnitTimestamps(wu, prepared.wu)

	if err := tx.WorkUnits.InsertWorkUnit(ctx, prepared.wu); err != nil {
		return nil, nil, fmt.Errorf("failed to insert work unit: %w", err)
	}

	exp, err := tx.Experiments.FetchExperiment(ctx, prepared.xid)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return nil, nil, fmt.Errorf("experiment %d not found", prepared.xid)
	}

	if err := recordWorkUnitCreationHistory(ctx, tx, prepared.xid, prepared.wid, prepared.wu); err != nil {
		return nil, nil, err
	}

	exp, err = updateExperimentForWorkUnitCreation(ctx, tx.Experiments, exp, prepared.wu, now)
	if err != nil {
		return nil, nil, err
	}

	return prepared.wu, exp, nil
}

func prepareLocalWorkUnit(ctx context.Context, workUnits storage.WorkUnitTxRepository, wu *essmsgpb.WorkUnit, now time.Time) (preparedWorkUnit, error) {
	localWU := proto.Clone(wu).(*essmsgpb.WorkUnit)

	xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(localWU.GetName())
	if err != nil {
		return preparedWorkUnit{}, fmt.Errorf("failed to validate work unit name: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}

	if wid == 0 {
		wid, err = workUnits.FetchNextWorkUnitID(ctx, xid)
		if err != nil {
			return preparedWorkUnit{}, fmt.Errorf("failed to fetch next work unit ID: %w", err)
		}
		localWU.Name = validator.ConstructWorkUnitName(xid, wid)
		localWU.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialWorkUnitExecutionID)
		setParentIfNonNil(localWU.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage(), localWU.GetName())
	}

	ts := timestamppb.New(now)
	localWU.CreateTime = ts
	localWU.UpdateTime = ts
	localWU.CurrentWorkUnitExecution.CreateTime = ts
	localWU.CurrentWorkUnitExecution.UpdateTime = ts
	localWU.CurrentWorkUnitExecution.Status.UpdateTime = ts
	localWU.CurrentWorkUnitExecution.Status.StateUpdateTime = ts

	return preparedWorkUnit{wu: localWU, xid: xid, wid: wid}, nil
}

func writeBackWorkUnitTimestamps(wu, localWU *essmsgpb.WorkUnit) {
	wu.CreateTime = localWU.CreateTime
	wu.UpdateTime = localWU.UpdateTime
	if wu.CurrentWorkUnitExecution == nil {
		return
	}
	wu.CurrentWorkUnitExecution.CreateTime = localWU.CurrentWorkUnitExecution.CreateTime
	wu.CurrentWorkUnitExecution.UpdateTime = localWU.CurrentWorkUnitExecution.UpdateTime
	if wu.CurrentWorkUnitExecution.Status == nil {
		return
	}
	wu.CurrentWorkUnitExecution.Status.UpdateTime = localWU.CurrentWorkUnitExecution.Status.UpdateTime
	wu.CurrentWorkUnitExecution.Status.StateUpdateTime = localWU.CurrentWorkUnitExecution.Status.StateUpdateTime
}

func recordWorkUnitCreationHistory(ctx context.Context, tx *storage.Transaction, xid, wid int64, wu *essmsgpb.WorkUnit) error {
	initialEID := initialWorkUnitExecutionID
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &wid, &initialEID, nil, wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save system status message history: %w", err)
	}

	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(wu.GetExecutable())
	resMap := acc.IntTotals()
	qName := wu.GetExecutable().GetSchedulingConstraints().GetQueueName()
	var newState *essmsgpb.WorkUnitStatus_WorkUnitState
	var stateTime time.Time
	if status := wu.GetCurrentWorkUnitExecution().GetStatus(); status != nil {
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

func updateExperimentForWorkUnitCreation(ctx context.Context, experiments storage.ExperimentTxRepository, exp *essmsgpb.Experiment, wu *essmsgpb.WorkUnit, now time.Time) (*essmsgpb.Experiment, error) {
	creationRes, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, wu, now)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
	}

	// TODO(b/517479143): Consolidate UpdateExperimentStatus and UpdateExperimentResources into a single mutation write to prevent duplicate row updates for the same Experiments row in Spanner.
	if err := experiments.UpdateExperimentStatus(ctx, creationRes.Experiment); err != nil {
		return nil, fmt.Errorf("failed to update experiment status: %w", err)
	}
	if creationRes.ResourcesChanged {
		if err := experiments.UpdateExperimentResources(ctx, creationRes.Experiment); err != nil {
			return nil, fmt.Errorf("failed to update experiment resources: %w", err)
		}
	}
	return creationRes.Experiment, nil
}
