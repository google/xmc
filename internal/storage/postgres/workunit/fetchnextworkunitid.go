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

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// nextWorkUnitIDSQL computes the next free work unit ID of an experiment. The aggregate cannot be
// locked, so callers lock the parent experiment row first.
var nextWorkUnitIDSQL = safesql.New(`SELECT COALESCE(MAX(WorkUnitId), 0) + 1 FROM WorkUnits WHERE Xid = $1`)

// FetchNextWorkUnitID retrieves the next available work unit ID for experiment xid within the
// transaction: one more than the highest existing ID, or 1 for an experiment without work units.
// It first locks the parent experiment row until the transaction ends, so that concurrent
// transactions creating work units for the same experiment allocate distinct IDs, and fails with
// NotFound when the experiment does not exist.
func (r *TxRepository) FetchNextWorkUnitID(ctx context.Context, xid int64) (int64, error) {
	if err := r.requireTx(); err != nil {
		return 0, err
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Fetch next WorkUnit ID from storage")

	if err := r.lockExperiment(ctx, xid); err != nil {
		return 0, err
	}
	var nextID int64
	if err := r.tx.QueryRow(ctx, nextWorkUnitIDSQL.String(), xid).Scan(&nextID); err != nil {
		logging.Error(ctx, "Failed to read next WorkUnit ID", "error", err)
		return 0, pgdb.TranslateError(err, workUnitEntity)
	}

	logging.Info(ctx, "Fetched next WorkUnit ID from storage", "workUnitID", nextID)
	return nextID, nil
}
