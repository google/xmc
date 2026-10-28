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
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// updateWorkUnitSQL writes the mutable work unit columns, the counterpart of the Spanner
// WorkUnitUpdateColumns mutation.
var updateWorkUnitSQL = safesql.New(`UPDATE WorkUnits SET
  Priority = $3,
  Urls = $4,
  Tags = $5,
  Notes = $6,
  UpdateTime = $7
WHERE Xid = $1 AND WorkUnitId = $2`)

// UpdateWorkUnit updates a work unit with the given update mask in a transaction of its own that
// locks the work unit row while it merges the update, so that concurrent updates of the same work
// unit are applied one after another. It returns both the original and the updated work units in
// that order, so that the caller can choose whether to perform additional actions based on the
// original work unit state. When the merge leaves the work unit unchanged nothing is written and
// both returned work units are equal.
// TODO(b/517479143): Move work unit update merging logic outside the repository layer into a domain service layer.
func (r *Repository) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (storage.UpdatedWorkUnits, error) {
	if update == nil {
		return storage.UpdatedWorkUnits{}, pgdb.WrapStatusError("update must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(update.XID, update.WID))
	logging.Info(ctx, "UpdateWorkUnit", "update", update.WorkUnit)

	var originalWorkUnit, updatedWorkUnit *essmsgpb.WorkUnit
	var mutated bool
	txErr := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// The callback may run more than once, so reset everything it sets.
		originalWorkUnit, updatedWorkUnit, mutated = nil, nil, false

		// 1. Read and lock the existing WorkUnit.
		var err error
		originalWorkUnit, err = fetchWorkUnit(ctx, tx, lockWorkUnitSQL, update.XID, update.WID)
		if err != nil {
			return err
		}

		// 2. Merge the current and new entries based on the update mask.
		updatedWorkUnit, err = data.MergeWorkUnits(originalWorkUnit, update.WorkUnit, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge WorkUnit: %w", err)
		}

		// 2.1. Check if the work unit has changed.
		if proto.Equal(originalWorkUnit, updatedWorkUnit) {
			return nil
		}

		// 2.2. Update the UpdateTime column to the current time, at the microsecond precision of
		// TIMESTAMPTZ so that the returned work unit matches the stored one.
		updatedWorkUnit.UpdateTime = timestamppb.New(time.Now().Truncate(time.Microsecond))

		// 3. Write the mutable columns.
		mutated = true
		return writeWorkUnitUpdate(ctx, tx, updatedWorkUnit)
	})
	if txErr != nil {
		return storage.UpdatedWorkUnits{}, txErr
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalWorkUnit), prototext.Format(updatedWorkUnit))
		logging.Info(ctx, fmt.Sprintf("WorkUnit updated successfully: %s", protoDiff), "updatedWorkUnit", updatedWorkUnit)
	} else {
		logging.Info(ctx, "WorkUnit not updated; no changes detected", "updatedWorkUnit", updatedWorkUnit)
	}

	return storage.UpdatedWorkUnits{
		Original: originalWorkUnit,
		Updated:  updatedWorkUnit,
	}, nil
}

// writeWorkUnitUpdate writes the mutable columns of workUnit, whose row the transaction has
// locked, mirroring spannerdata.WorkUnitProtoToUpdateMutation.
func writeWorkUnitUpdate(ctx context.Context, tx pgdb.DBTX, workUnit *essmsgpb.WorkUnit) error {
	xid, wid, err := validator.ValidateWorkUnitName(workUnit.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate work unit name %q: %w", workUnit.GetName(), err)
	}
	urls, err := marshalURLs(workUnit.GetUrls())
	if err != nil {
		return fmt.Errorf("failed to create update for WorkUnit: %w", err)
	}
	tag, err := tx.Exec(ctx, updateWorkUnitSQL.String(),
		xid, wid, int64(workUnit.GetPriority()), urls, tagsOrEmpty(workUnit.GetTags()), workUnit.GetNotes(), workUnit.GetUpdateTime().AsTime())
	if err != nil {
		return pgdb.TranslateError(err, workUnitEntityName(xid, wid))
	}
	if tag.RowsAffected() == 0 {
		return pgdb.WrapStatusError(fmt.Sprintf("%s not found", workUnitEntityName(xid, wid)), grpcerr.ErrNotFound, nil)
	}
	return nil
}
