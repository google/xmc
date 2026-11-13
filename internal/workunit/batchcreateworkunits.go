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
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// initialExecutionID is the execution ID of the first execution of a work unit.
const initialExecutionID = int64(1)

// ErrBatchCreateRequiresStudy is returned when work units are batch created on an experiment that
// is not a STUDY.
var ErrBatchCreateRequiresStudy = status.Error(codes.Unimplemented, "BatchCreateWorkUnits is only supported on STUDY experiments")

// BatchCreateWorkUnits creates wus as new work units of the STUDY experiment xid in a single
// transaction, after checking within it that creator is the study controller (see
// AuthorizeStudyController). The work units get the contiguous IDs that follow the experiment's
// largest work unit ID, in order, and the Experiment row is read and written once. It returns the
// created work units in the order of wus; wus is not modified.
func BatchCreateWorkUnits(ctx context.Context, txRunner storage.TransactionRunner, xid int64, creator string, wus []*essmsgpb.WorkUnit) ([]*essmsgpb.WorkUnit, error) {
	var created []*essmsgpb.WorkUnit
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		created, err = batchCreateWorkUnitsInTx(ctx, tx, xid, creator, wus, time.Now())
		return err
	})
	if err != nil {
		logging.Error(ctx, "Failed to batch create work units", "count", len(wus), "error", err)
		return nil, err
	}
	logging.Info(ctx, "Batch created work units", "count", len(created))
	return created, nil
}

// batchCreateWorkUnitsInTx is the body of the BatchCreateWorkUnits transaction. It only writes
// through tx, so it is safe to re-run when the transaction is retried.
func batchCreateWorkUnitsInTx(ctx context.Context, tx *storage.Transaction, xid int64, creator string, wus []*essmsgpb.WorkUnit, now time.Time) ([]*essmsgpb.WorkUnit, error) {
	if err := AuthorizeStudyController(ctx, tx.Components, creator); err != nil {
		return nil, err
	}
	exp, err := fetchExperimentInTx(ctx, tx, xid)
	if err != nil {
		return nil, err
	}
	if exp.GetExperimentType() != essmsgpb.Experiment_STUDY {
		return nil, fmt.Errorf("experiment %d: %w", xid, ErrBatchCreateRequiresStudy)
	}
	firstWID, err := tx.WorkUnits.FetchNextWorkUnitID(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch next work unit ID: %w", err)
	}
	created := newBatchWorkUnits(wus, xid, firstWID, now)
	if err := tx.WorkUnits.InsertWorkUnits(ctx, created); err != nil {
		return nil, fmt.Errorf("failed to insert work units: %w", err)
	}
	for i, wu := range created {
		if err := RecordExecutionHistory(ctx, tx, xid, firstWID+int64(i), initialExecutionID, wu.GetExecutable(), nil, wu.GetCurrentWorkUnitExecution()); err != nil {
			return nil, err
		}
	}
	if _, err := applyWorkUnitCreations(ctx, tx, exp, created, now); err != nil {
		return nil, err
	}
	return created, nil
}

// newBatchWorkUnits returns copies of wus named as work units firstWID, firstWID+1, ... of
// experiment xid, each with its initial execution and timestamps set to now.
func newBatchWorkUnits(wus []*essmsgpb.WorkUnit, xid, firstWID int64, now time.Time) []*essmsgpb.WorkUnit {
	created := make([]*essmsgpb.WorkUnit, len(wus))
	for i, wu := range wus {
		wid := firstWID + int64(i)
		local := proto.Clone(wu).(*essmsgpb.WorkUnit)
		ensureWorkUnitExecutionStatus(local)
		local.Name = validator.ConstructWorkUnitName(xid, wid)
		local.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, initialExecutionID)
		setParentIfNonNil(local.CurrentWorkUnitExecution.Status.SystemMessage, local.GetName())
		setParentIfNonNil(local.CurrentWorkUnitExecution.Status.UserMessage, local.GetName())
		stampNewWorkUnit(local, now)
		created[i] = local
	}
	return created
}
