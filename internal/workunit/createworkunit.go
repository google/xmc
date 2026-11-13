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
	experimentdomain "xmanager_cloud/internal/domain/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

func assignWorkUnitMetadataAndTimestamps(ctx context.Context, tx *storage.Transaction, wu *essmsgpb.WorkUnit, now time.Time) (int64, int64, error) {
	xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
	if err != nil {
		return 0, 0, fmt.Errorf("failed to validate work unit name: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	ensureWorkUnitExecutionStatus(wu)
	if wid == 0 {
		wid, err = tx.WorkUnits.FetchNextWorkUnitID(ctx, xid)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to fetch next work unit ID: %w", err)
		}
		wu.Name = validator.ConstructWorkUnitName(xid, wid)
		wu.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, 1)
	}
	setParentIfNonNil(wu.CurrentWorkUnitExecution.Status.SystemMessage, wu.GetName())
	setParentIfNonNil(wu.CurrentWorkUnitExecution.Status.UserMessage, wu.GetName())

	wu.CreateTime, wu.UpdateTime = timestamppb.New(now), timestamppb.New(now)
	wu.CurrentWorkUnitExecution.CreateTime, wu.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now), timestamppb.New(now)
	wu.CurrentWorkUnitExecution.Status.UpdateTime, wu.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now), timestamppb.New(now)
	return xid, wid, nil
}

func insertWorkUnitAndSyncExperiment(ctx context.Context, tx *storage.Transaction, localWU *essmsgpb.WorkUnit, xid, wid int64, now time.Time) (*essmsgpb.Experiment, error) {
	if err := tx.WorkUnits.InsertWorkUnit(ctx, localWU); err != nil {
		return nil, fmt.Errorf("failed to insert work unit: %w", err)
	}
	exp, err := tx.Experiments.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return nil, fmt.Errorf("experiment %d not found", xid)
	}
	if err := RecordExecutionHistory(ctx, tx, xid, wid, 1, localWU.GetExecutable(), nil, localWU.GetCurrentWorkUnitExecution()); err != nil {
		return nil, err
	}
	creationRes, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, localWU, now)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
	}
	// TODO: Consolidate UpdateExperimentStatus and UpdateExperimentResources into a single mutation write to prevent duplicate row updates for the same Experiments row in Spanner.
	if err := tx.Experiments.UpdateExperimentStatus(ctx, creationRes.Experiment); err != nil {
		return nil, fmt.Errorf("failed to update experiment status: %w", err)
	}
	if creationRes.ResourcesChanged {
		if err := tx.Experiments.UpdateExperimentResources(ctx, creationRes.Experiment); err != nil {
			return nil, fmt.Errorf("failed to update experiment resources: %w", err)
		}
	}
	return creationRes.Experiment, nil
}

func copyCommittedWorkUnitTimestamps(dst, src *essmsgpb.WorkUnit) {
	if dst == nil || src == nil {
		return
	}
	dst.CreateTime, dst.UpdateTime = src.CreateTime, src.UpdateTime
	if dst.CurrentWorkUnitExecution == nil || src.CurrentWorkUnitExecution == nil {
		return
	}
	dst.CurrentWorkUnitExecution.CreateTime = src.CurrentWorkUnitExecution.CreateTime
	dst.CurrentWorkUnitExecution.UpdateTime = src.CurrentWorkUnitExecution.UpdateTime
	if dst.CurrentWorkUnitExecution.Status != nil && src.CurrentWorkUnitExecution.Status != nil {
		dst.CurrentWorkUnitExecution.Status.UpdateTime = src.CurrentWorkUnitExecution.Status.UpdateTime
		dst.CurrentWorkUnitExecution.Status.StateUpdateTime = src.CurrentWorkUnitExecution.Status.StateUpdateTime
	}
}

// CreateWorkUnit creates a new work unit in storage.
func CreateWorkUnit(ctx context.Context, txRunner storage.TransactionRunner, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if txRunner == nil {
		return nil, fmt.Errorf("transaction runner cannot be nil: %w", grpcerr.ErrInternal)
	}
	ctx = logging.WithWorkUnitExecutionName(ctx, wu.GetCurrentWorkUnitExecution().GetName())
	logging.Info(ctx, "Write work unit to storage", "work_unit", wu)

	exp := &essmsgpb.Experiment{}
	var committedWU *essmsgpb.WorkUnit
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		now := time.Now()
		localWU := proto.Clone(wu).(*essmsgpb.WorkUnit)
		xid, wid, err := assignWorkUnitMetadataAndTimestamps(ctx, tx, localWU, now)
		if err != nil {
			return err
		}
		updatedExp, err := insertWorkUnitAndSyncExperiment(ctx, tx, localWU, xid, wid, now)
		if err != nil {
			return err
		}
		exp = updatedExp
		committedWU = localWU
		return nil
	})
	if err != nil {
		logging.Error(ctx, "failed to create work unit", "work_unit", wu, "error", err)
		return nil, err
	}

	// Populate timestamps back onto caller's proto pointer (matching CreateExperiment behavior).
	copyCommittedWorkUnitTimestamps(wu, committedWU)
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
