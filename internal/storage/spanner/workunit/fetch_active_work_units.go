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
	"googlesql/safesql/go/safesql"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/validator/validator"
)

const (
	defaultPageSize = int32(100)
	maxPageSize     = int32(1000)
)

var fetchActiveWorkUnitPKsSQL = safesql.New(
	`SELECT BitReversedXid, WorkUnitId
	FROM WorkUnits wu
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL
	ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC
	LIMIT @limit OFFSET @offset`,
)

var countActiveWorkUnitsSQL = safesql.New(
	`SELECT COUNT(*)
	FROM WorkUnits wu
	WHERE wu.QueueName IN UNNEST(@queueNames)
	  AND wu.IsPendingOrRunningJob IS NOT NULL`,
)

// ActiveWorkUnitsResult holds the result of querying active work units.
type ActiveWorkUnitsResult struct {
	WorkUnits      []*essmsgpb.WorkUnit
	NextPageToken  string
	TotalQueueSize int64
}

type workUnitPK struct {
	BitReversedXid int64
	WorkUnitId     int64
}

func decodePageToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	var offset int64
	if _, err := fmt.Sscanf(token, "%d", &offset); err != nil {
		return 0, fmt.Errorf("invalid page_token format: %w", err)
	}
	return offset, nil
}

func encodePageToken(offset int64) string {
	return fmt.Sprintf("%d", offset)
}

func fetchActiveWorkUnitPKs(ctx context.Context, txn *spanner.ReadOnlyTransaction, queueNames []string, limit int64, offset int64) ([]workUnitPK, []spanner.KeySet, error) {
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
	var keys []spanner.Key
	if err := iter.Do(func(row *spanner.Row) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var bitReversedXid, workUnitId int64
		if err := row.ColumnByName("BitReversedXid", &bitReversedXid); err != nil {
			return fmt.Errorf("failed to read column BitReversedXid: %w", err)
		}
		if err := row.ColumnByName("WorkUnitId", &workUnitId); err != nil {
			return fmt.Errorf("failed to read column WorkUnitId: %w", err)
		}
		pk := workUnitPK{
			BitReversedXid: bitReversedXid,
			WorkUnitId:     workUnitId,
		}
		orderedPKs = append(orderedPKs, pk)
		keys = append(keys, spanner.Key{bitReversedXid, workUnitId})
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to query active work unit primary keys: %w", err)
	}

	if len(orderedPKs) == 0 {
		return nil, nil, nil
	}

	keySets := make([]spanner.KeySet, len(keys))
	for i, k := range keys {
		keySets[i] = k
	}
	keySetList := []spanner.KeySet{spanner.KeySets(keySets...)}
	return orderedPKs, keySetList, nil
}

func batchReadWorkUnits(ctx context.Context, txn *spanner.ReadOnlyTransaction, keySetList []spanner.KeySet, orderedPKs []workUnitPK) ([]*essmsgpb.WorkUnit, error) {
	workUnitMap := make(map[workUnitPK]*essmsgpb.WorkUnit)
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
			pk := workUnitPK{
				BitReversedXid: spannerutils.ReverseBits(xid),
				WorkUnitId:     wid,
			}
			workUnitMap[pk] = wu
			return nil
		})
		iter.Stop()
		if err != nil {
			return nil, fmt.Errorf("failed to batch read WorkUnit rows: %w", err)
		}
	}

	workUnits := make([]*essmsgpb.WorkUnit, 0, len(orderedPKs))
	for _, pk := range orderedPKs {
		wu, ok := workUnitMap[pk]
		if !ok {
			return nil, fmt.Errorf("work unit with PK (BitReversedXid: %d, WorkUnitId: %d) missing from batch read result", pk.BitReversedXid, pk.WorkUnitId)
		}
		workUnits = append(workUnits, wu)
	}

	return workUnits, nil
}

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*ActiveWorkUnitsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if r == nil || r.client == nil {
		return nil, fmt.Errorf("spanner storage client is uninitialized")
	}

	if len(queueNames) == 0 {
		return &ActiveWorkUnitsResult{
			WorkUnits:      []*essmsgpb.WorkUnit{},
			NextPageToken:  "",
			TotalQueueSize: 0,
		}, nil
	}

	pageSizeVal := pageSize
	if pageSizeVal <= 0 {
		pageSizeVal = defaultPageSize
	} else if pageSizeVal > maxPageSize {
		pageSizeVal = maxPageSize
	}

	offset, err := decodePageToken(pageToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w", err)
	}

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	// Query 1: Count total active work units across matching queues
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
		logging.Error(ctx, "failed to count active work units", "error", err)
		return nil, fmt.Errorf("failed to count active work units: %w", err)
	}

	// Query 2: Fetch pageSize + 1 items to determine if nextPageToken is needed
	limit := int64(pageSizeVal) + 1
	orderedPKs, keySetList, err := fetchActiveWorkUnitPKs(ctx, txn, queueNames, limit, offset)
	if err != nil {
		logging.Error(ctx, "failed to fetch active work unit primary keys", "error", err)
		return nil, err
	}

	if len(orderedPKs) == 0 {
		return &ActiveWorkUnitsResult{
			WorkUnits:      []*essmsgpb.WorkUnit{},
			NextPageToken:  "",
			TotalQueueSize: totalQueueSize,
		}, nil
	}

	nextPageToken := ""
	if int64(len(orderedPKs)) > int64(pageSizeVal) {
		orderedPKs = orderedPKs[:pageSizeVal]
		nextPageToken = encodePageToken(offset + int64(pageSizeVal))
	}

	workUnits, err := batchReadWorkUnits(ctx, txn, keySetList, orderedPKs)
	if err != nil {
		logging.Error(ctx, "failed to batch read work units", "error", err)
		return nil, err
	}

	return &ActiveWorkUnitsResult{
		WorkUnits:      workUnits,
		NextPageToken:  nextPageToken,
		TotalQueueSize: totalQueueSize,
	}, nil
}
