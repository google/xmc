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

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
)

var fetchActiveWorkUnitResourcesSQL = safesql.New(
	`SELECT e.Creator, wu.Executable, wu.ExecutionState
	FROM WorkUnits wu
	JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL`,
)

// FetchActiveWorkUnitResources reads the creator, executable and state of every active work unit
// in queueNames in a single read.
func (r *Repository) FetchActiveWorkUnitResources(ctx context.Context, queueNames []string) ([]storage.ActiveWorkUnitResources, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("spanner storage client is uninitialized: %w", grpcerr.ErrFailedPrecondition)
	}
	return fetchActiveWorkUnitResourcesInTxn(ctx, r.client.Single(), queueNames)
}

func fetchActiveWorkUnitResourcesInTxn(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string) ([]storage.ActiveWorkUnitResources, error) {
	result := []storage.ActiveWorkUnitResources{}
	if len(queueNames) == 0 {
		return result, nil
	}
	stmt := spanner.Statement{
		SQL:    fetchActiveWorkUnitResourcesSQL.String(),
		Params: map[string]any{"queueNames": queueNames},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()
	err := iter.Do(func(row *spanner.Row) error {
		var wu storage.ActiveWorkUnitResources
		if err := row.Columns(&wu.Creator, &wu.Executable, &wu.ExecutionState); err != nil {
			return fmt.Errorf("failed to read active work unit resources row: %w", err)
		}
		result = append(result, wu)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query active work unit resources: %w", err)
	}
	return result, nil
}
