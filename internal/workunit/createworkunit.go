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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	experimentdomain "xmanager_cloud/internal/domain/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// ErrStudyWorkUnitCreation is returned when a single work unit is created on a STUDY experiment.
// Study trials are created only by the study controller, which keeps the trial count equal to the
// persisted work unit count.
var ErrStudyWorkUnitCreation = status.Error(codes.FailedPrecondition, "work units of a study experiment are created by the study controller")

// parseNewWorkUnitName returns the experiment ID and work unit ID of a new work unit's name. A
// zero work unit ID asks storage to assign the next one.
func parseNewWorkUnitName(name string) (int64, int64, error) {
	xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(name)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to validate work unit name: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}
	return xid, wid, nil
}

// assignWorkUnitMetadataAndTimestamps assigns the work unit ID when wid is zero, then sets the
// message parents and creation timestamps. It returns the final work unit ID.
func assignWorkUnitMetadataAndTimestamps(ctx context.Context, tx *storage.Transaction, wu *essmsgpb.WorkUnit, xid, wid int64, now time.Time) (int64, error) {
	ensureWorkUnitExecutionStatus(wu)
	if wid == 0 {
		var err error
		wid, err = tx.WorkUnits.FetchNextWorkUnitID(ctx, xid)
		if err != nil {
			return 0, fmt.Errorf("failed to fetch next work unit ID: %w", err)
		}
		wu.Name = validator.ConstructWorkUnitName(xid, wid)
		wu.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, 1)
	}
	setParentIfNonNil(wu.CurrentWorkUnitExecution.Status.SystemMessage, wu.GetName())
	setParentIfNonNil(wu.CurrentWorkUnitExecution.Status.UserMessage, wu.GetName())
	stampNewWorkUnit(wu, now)
	return wid, nil
}

// stampNewWorkUnit sets the creation and update timestamps of a new work unit, its current
// execution, and its status to now.
func stampNewWorkUnit(wu *essmsgpb.WorkUnit, now time.Time) {
	wu.CreateTime, wu.UpdateTime = timestamppb.New(now), timestamppb.New(now)
	wu.CurrentWorkUnitExecution.CreateTime, wu.CurrentWorkUnitExecution.UpdateTime = timestamppb.New(now), timestamppb.New(now)
	wu.CurrentWorkUnitExecution.Status.UpdateTime, wu.CurrentWorkUnitExecution.Status.StateUpdateTime = timestamppb.New(now), timestamppb.New(now)
}

// insertWorkUnitAndSyncExperiment inserts the work unit and updates its parent experiment exp,
// which the caller has already read in this transaction.
func insertWorkUnitAndSyncExperiment(ctx context.Context, tx *storage.Transaction, localWU *essmsgpb.WorkUnit, exp *essmsgpb.Experiment, xid, wid int64, now time.Time) (*essmsgpb.Experiment, error) {
	if err := tx.WorkUnits.InsertWorkUnit(ctx, localWU); err != nil {
		return nil, fmt.Errorf("failed to insert work unit: %w", err)
	}
	if err := RecordExecutionHistory(ctx, tx, xid, wid, 1, localWU.GetExecutable(), nil, localWU.GetCurrentWorkUnitExecution()); err != nil {
		return nil, err
	}
	return applyWorkUnitCreations(ctx, tx, exp, []*essmsgpb.WorkUnit{localWU}, now)
}

// applyWorkUnitCreations folds the creation of wus into the denormalized status and resources of
// their parent experiment exp in memory, then writes the Experiment row once. It returns the
// updated experiment.
func applyWorkUnitCreations(ctx context.Context, tx *storage.Transaction, exp *essmsgpb.Experiment, wus []*essmsgpb.WorkUnit, now time.Time) (*essmsgpb.Experiment, error) {
	resourcesChanged := false
	for _, wu := range wus {
		creationRes, err := experimentdomain.PrepareWorkUnitCreation(ctx, exp, wu, now)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare experiment status for work unit creation: %w", err)
		}
		exp = creationRes.Experiment
		resourcesChanged = resourcesChanged || creationRes.ResourcesChanged
	}
	// TODO: Consolidate UpdateExperimentStatus and UpdateExperimentResources into a single mutation write to prevent duplicate row updates for the same Experiments row in Spanner.
	if err := tx.Experiments.UpdateExperimentStatus(ctx, exp); err != nil {
		return nil, fmt.Errorf("failed to update experiment status: %w", err)
	}
	if resourcesChanged {
		if err := tx.Experiments.UpdateExperimentResources(ctx, exp); err != nil {
			return nil, fmt.Errorf("failed to update experiment resources: %w", err)
		}
	}
	return exp, nil
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

// fetchWorkUnitParent reads the parent experiment of a single new work unit. It returns
// ErrStudyWorkUnitCreation for a STUDY, whose trials only the study controller creates.
func fetchWorkUnitParent(ctx context.Context, tx *storage.Transaction, xid int64) (*essmsgpb.Experiment, error) {
	exp, err := fetchExperimentInTx(ctx, tx, xid)
	if err != nil {
		return nil, err
	}
	if exp.GetExperimentType() == essmsgpb.Experiment_STUDY {
		return nil, fmt.Errorf("experiment %d is a study: %w", xid, ErrStudyWorkUnitCreation)
	}
	return exp, nil
}

// fetchExperimentInTx reads experiment xid within tx and fails when it does not exist.
func fetchExperimentInTx(ctx context.Context, tx *storage.Transaction, xid int64) (*essmsgpb.Experiment, error) {
	exp, err := tx.Experiments.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return nil, fmt.Errorf("experiment %d not found", xid)
	}
	return exp, nil
}

// CreateWorkUnit creates a new work unit in storage. It rejects STUDY experiments with
// ErrStudyWorkUnitCreation.
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
		xid, wid, err := parseNewWorkUnitName(localWU.GetName())
		if err != nil {
			return err
		}
		// Read and check the parent first: a rejected or missing parent allocates no ID and
		// buffers no write, and the experiment row is locked before the work unit rows.
		parent, err := fetchWorkUnitParent(ctx, tx, xid)
		if err != nil {
			return err
		}
		wid, err = assignWorkUnitMetadataAndTimestamps(ctx, tx, localWU, xid, wid, now)
		if err != nil {
			return err
		}
		updatedExp, err := insertWorkUnitAndSyncExperiment(ctx, tx, localWU, parent, xid, wid, now)
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
