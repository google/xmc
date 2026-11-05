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

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/offsetpagetoken"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

var fetchActiveWorkUnitPKsSQL = safesql.New(
	`SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator, COALESCE(e.Title, '') AS ExperimentTitle
	FROM WorkUnits wu
	JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL
	ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC
	LIMIT @limit OFFSET @offset`,
)

var countActiveWorkUnitsSQL = safesql.New(
	`SELECT COUNT(*)
	FROM WorkUnits wu
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL`,
)

type workUnitPK struct {
	BitReversedXid  int64
	WorkUnitId      int64
	Creator         string
	ExperimentTitle string
}

func fetchActiveWorkUnitPKs(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string, limit int64, offset int64) ([]workUnitPK, error) {
	stmt := spanner.Statement{
		SQL: fetchActiveWorkUnitPKsSQL.String(),
		Params: map[string]interface{}{
			"queueNames": queueNames,
			"limit":      limit,
			"offset":     offset,
		},
	}

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var orderedPKs []workUnitPK
	if err := iter.Do(func(row *spanner.Row) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var bitReversedXid, workUnitId int64
		var creator, experimentTitle string
		if err := row.ColumnByName("BitReversedXid", &bitReversedXid); err != nil {
			return fmt.Errorf("failed to read column BitReversedXid: %w", err)
		}
		if err := row.ColumnByName("WorkUnitId", &workUnitId); err != nil {
			return fmt.Errorf("failed to read column WorkUnitId: %w", err)
		}
		if err := row.ColumnByName("Creator", &creator); err != nil {
			return fmt.Errorf("failed to read column Creator: %w", err)
		}
		if err := row.ColumnByName("ExperimentTitle", &experimentTitle); err != nil {
			return fmt.Errorf("failed to read column ExperimentTitle: %w", err)
		}
		pk := workUnitPK{
			BitReversedXid:  bitReversedXid,
			WorkUnitId:      workUnitId,
			Creator:         creator,
			ExperimentTitle: experimentTitle,
		}
		orderedPKs = append(orderedPKs, pk)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to query active work unit primary keys: %w", err)
	}

	return orderedPKs, nil
}

func batchReadWorkUnits(ctx context.Context, txn *spanner.ReadOnlyTransaction, keySetList []spanner.KeySet, orderedPKs []workUnitPK) ([]storage.ActiveWorkUnit, error) {
	type workUnitKey struct {
		bitReversedXid int64
		workUnitId     int64
	}
	workUnitMap := make(map[workUnitKey]*essmsgpb.WorkUnit)
	for _, keySet := range keySetList {
		iter := txn.Read(ctx, workunitschema.WorkUnitTableName, keySet, workunitschema.WorkUnitColumns)
		err := iter.Do(func(row *spanner.Row) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			wu, err := spannerdata.WorkUnitRowToProto(row)
			if err != nil {
				return fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
			}
			xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
			if err != nil {
				return fmt.Errorf("failed to validate WorkUnit name %q: %w", wu.GetName(), err)
			}
			key := workUnitKey{
				bitReversedXid: spannerutils.ReverseBits(xid),
				workUnitId:     wid,
			}
			workUnitMap[key] = wu
			return nil
		})
		iter.Stop()
		if err != nil {
			return nil, fmt.Errorf("failed to batch read WorkUnit rows: %w", err)
		}
	}

	workUnits := make([]storage.ActiveWorkUnit, 0, len(orderedPKs))
	for _, pk := range orderedPKs {
		key := workUnitKey{
			bitReversedXid: pk.BitReversedXid,
			workUnitId:     pk.WorkUnitId,
		}
		wu, ok := workUnitMap[key]
		if !ok {
			return nil, fmt.Errorf("work unit with PK (BitReversedXid: %d, WorkUnitId: %d) missing from batch read result", pk.BitReversedXid, pk.WorkUnitId)
		}
		workUnits = append(workUnits, storage.ActiveWorkUnit{
			WorkUnit:        wu,
			Author:          pk.Creator,
			ExperimentTitle: pk.ExperimentTitle,
		})
	}

	return workUnits, nil
}

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	var res *storage.ActiveWorkUnitsResult
	err := r.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, repo storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		res, innerErr = repo.FetchActiveWorkUnitsByQueues(txCtx, queueNames, pageSize, pageToken)
		return innerErr
	})
	return res, err
}

func fetchActiveWorkUnitsInTxn(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(queueNames) == 0 {
		return &storage.ActiveWorkUnitsResult{WorkUnits: []storage.ActiveWorkUnit{}}, nil
	}
	pageSizeVal := offsetpagetoken.ClampPageSize(pageSize)
	offset, err := offsetpagetoken.Decode(pageToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w", err)
	}
	totalQueueSize, err := countActiveWorkUnits(ctx, txn, queueNames)
	if err != nil {
		logging.Error(ctx, "failed to count active work units", "error", err)
		return nil, err
	}
	orderedPKs, err := fetchActiveWorkUnitPKs(ctx, txn, queueNames, int64(pageSizeVal)+1, offset)
	if err != nil {
		logging.Error(ctx, "failed to fetch active work unit primary keys", "error", err)
		return nil, err
	}
	return readActiveWorkUnitsPage(ctx, txn, orderedPKs, pageSizeVal, offset, totalQueueSize)
}

func readActiveWorkUnitsPage(ctx context.Context, txn *spanner.ReadOnlyTransaction, orderedPKs []workUnitPK, pageSizeVal int32, offset, totalQueueSize int64) (*storage.ActiveWorkUnitsResult, error) {
	if len(orderedPKs) == 0 {
		return &storage.ActiveWorkUnitsResult{
			WorkUnits:      []storage.ActiveWorkUnit{},
			TotalQueueSize: totalQueueSize,
		}, nil
	}
	nextPageToken := ""
	if int64(len(orderedPKs)) > int64(pageSizeVal) {
		orderedPKs = orderedPKs[:pageSizeVal]
		nextPageToken = offsetpagetoken.Encode(offset + int64(pageSizeVal))
	}
	keys := make([]spanner.KeySet, len(orderedPKs))
	for i, pk := range orderedPKs {
		keys[i] = spanner.Key{pk.BitReversedXid, pk.WorkUnitId}
	}
	workUnits, err := batchReadWorkUnits(ctx, txn, []spanner.KeySet{spanner.KeySets(keys...)}, orderedPKs)
	if err != nil {
		logging.Error(ctx, "failed to batch read work units", "error", err)
		return nil, err
	}
	return &storage.ActiveWorkUnitsResult{
		WorkUnits:      workUnits,
		NextPageToken:  nextPageToken,
		TotalQueueSize: totalQueueSize,
	}, nil
}

func countActiveWorkUnits(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string) (int64, error) {
	countStmt := spanner.Statement{
		SQL: countActiveWorkUnitsSQL.String(),
		Params: map[string]interface{}{
			"queueNames": queueNames,
		},
	}
	var totalQueueSize int64
	iterCount := txn.Query(ctx, countStmt)
	defer iterCount.Stop()
	if err := iterCount.Do(func(row *spanner.Row) error {
		return row.Column(0, &totalQueueSize)
	}); err != nil {
		return 0, fmt.Errorf("failed to count active work units: %w", err)
	}
	return totalQueueSize, nil
}
