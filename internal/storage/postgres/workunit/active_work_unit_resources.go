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

	"github.com/jackc/pgx/v5"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

type activeWorkUnitResourcesRow struct {
	Executable     []byte `db:"executable"`
	ExecutionState int64  `db:"executionstate"`
	Creator        string `db:"creator"`
}

var fetchActiveWorkUnitResourcesSQL = safesql.New(`SELECT wu.Executable, wu.ExecutionState, e.Creator
FROM WorkUnits wu
JOIN Experiments e ON wu.Xid = e.Xid
WHERE wu.QueueName = ANY($1) AND wu.IsPendingOrRunningJob IS NOT NULL`)

// FetchActiveWorkUnitsByQueues retrieves one page of active work units within the bound transaction.
func (r *TxRepository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	return fetchActiveWorkUnitsFromDB(ctx, r.tx, queueNames, pageSize, pageToken)
}

// FetchActiveWorkUnitResources reads the resources of active work units within the bound transaction.
func (r *TxRepository) FetchActiveWorkUnitResources(ctx context.Context, queueNames []string) ([]storage.ActiveWorkUnitResources, error) {
	return fetchActiveWorkUnitResourcesFromDB(ctx, r.tx, queueNames)
}

// WithReadOnlyWorkUnitSnapshot executes fn in a single PostgreSQL REPEATABLE READ READ ONLY transaction.
func (r *Repository) WithReadOnlyWorkUnitSnapshot(ctx context.Context, fn func(ctx context.Context, repo storage.WorkUnitSnapshotRepository) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.pool == nil {
		return pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return pgdb.RunInReadOnlyTx(ctx, r.pool, func(txCtx context.Context, tx pgx.Tx) error {
		return fn(txCtx, r.WithTx(tx))
	})
}

func fetchActiveWorkUnitResourcesFromDB(ctx context.Context, db pgdb.DBTX, queueNames []string) ([]storage.ActiveWorkUnitResources, error) {
	if len(queueNames) == 0 {
		return []storage.ActiveWorkUnitResources{}, nil
	}
	rows, err := pgdb.QueryRows[activeWorkUnitResourcesRow](ctx, db, fetchActiveWorkUnitResourcesSQL.String(), entityName, queueNames)
	if err != nil {
		return nil, err
	}
	result := make([]storage.ActiveWorkUnitResources, 0, len(rows))
	for _, row := range rows {
		exec, err := postgresdata.UnmarshalProto[essmsgpb.Executable](row.Executable)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Executable: %w", err)
		}
		result = append(result, storage.ActiveWorkUnitResources{
			Creator:        row.Creator,
			Executable:     exec,
			ExecutionState: essmsgpb.WorkUnitStatus_WorkUnitState(row.ExecutionState),
		})
	}
	return result, nil
}
