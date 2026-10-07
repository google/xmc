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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
)

// TODO(b/517479143): Optimize query to ORDER BY WorkUnitId DESC LIMIT 1 to perform a single-row index scan instead of MAX aggregation.
var nextWorkUnitIDSQL = safesql.New(`
	SELECT
		IFNULL(MAX(WorkUnitId), 0)
	FROM
		WorkUnits
	WHERE
		BitReversedXid = @bit_reversed_xid
`)

// fetchNextWorkUnitIDTransaction retrieves the next available WorkUnit ID for a given XID. This is
// a private method because the caller MUST provide their own transaction to avoid a race condition,
// so only the storage layer should call this method.
func (r *Repository) FetchNextWorkUnitIDTransaction(ctx context.Context, xid int64, txn spannerTransaction) (int64, error) {
	logging.Info(ctx, "Fetch next WorkUnit ID from storage")

	stmt := spanner.Statement{
		SQL:    nextWorkUnitIDSQL.String(),
		Params: map[string]any{"bit_reversed_xid": spannerutils.ReverseBits(xid)},
	}

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		logging.Error(ctx, "Failed to read WorkUnit row", "error", err)
		return 0, fmt.Errorf("failed to read WorkUnit row: %w", err)
	}

	var workUnitID int64
	if err := row.Column(0, &workUnitID); err != nil {
		logging.Error(ctx, "Failed to get WorkUnit ID from row", "error", err)
		return 0, fmt.Errorf("failed to get WorkUnit ID from row: %w", err)
	}

	logging.Info(ctx, "Fetched next WorkUnit ID from storage", "workUnitID", workUnitID+1)
	return workUnitID + 1, nil
}
