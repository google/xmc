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
	"xmanager_cloud/internal/storage/data"
)

var aggregateActiveResourcesSQL = safesql.New(
	`SELECT COALESCE(e.Creator, '') AS Creator, wu.Executable, wu.ExecutionState
	FROM WorkUnits wu
	JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL`,
)

// AggregateActiveResourcesByAuthor sums requested units by experiment creator, canonical resource
// type and state across all active work units of queueNames in one read-only transaction.
func (r *Repository) AggregateActiveResourcesByAuthor(ctx context.Context, queueNames []string) ([]storage.AuthorResourceAllocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("spanner storage client is uninitialized: %w", grpcerr.ErrFailedPrecondition)
	}
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()
	return aggregateActiveResourcesInTxn(ctx, txn, queueNames)
}

func aggregateActiveResourcesInTxn(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string) ([]storage.AuthorResourceAllocation, error) {
	if len(queueNames) == 0 {
		return []storage.AuthorResourceAllocation{}, nil
	}
	stmt := spanner.Statement{
		SQL:    aggregateActiveResourcesSQL.String(),
		Params: map[string]any{"queueNames": queueNames},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var samples []data.ActiveWorkUnitResourceSample
	if err := iter.Do(func(row *spanner.Row) error {
		sample, err := scanActiveResourceSample(row)
		if err != nil {
			return err
		}
		samples = append(samples, sample)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to query active work unit resources: %w", err)
	}
	return data.AggregateAuthorResourceSamples(samples), nil
}

func scanActiveResourceSample(row *spanner.Row) (data.ActiveWorkUnitResourceSample, error) {
	var sample data.ActiveWorkUnitResourceSample
	if err := row.ColumnByName("Creator", &sample.Creator); err != nil {
		return sample, fmt.Errorf("failed to read column Creator: %w", err)
	}
	if err := row.ColumnByName("Executable", &sample.Executable); err != nil {
		return sample, fmt.Errorf("failed to read column Executable: %w", err)
	}
	if err := row.ColumnByName("ExecutionState", &sample.ExecutionState); err != nil {
		return sample, fmt.Errorf("failed to read column ExecutionState: %w", err)
	}
	return sample, nil
}
