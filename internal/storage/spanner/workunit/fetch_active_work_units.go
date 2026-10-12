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
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	"cloud.google.com/go/spanner"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/workunitschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

const (
	defaultPageSize = int32(100)
	maxPageSize     = int32(1000)
)

var (
	// ErrInvalidPageToken is returned when the page token cannot be decoded or is invalid.
	ErrInvalidPageToken = errors.New("invalid page_token")
)

var fetchActiveWorkUnitPKsSQL = safesql.New(
	`SELECT wu.BitReversedXid, wu.WorkUnitId, COALESCE(e.Creator, '') AS Creator
	FROM WorkUnits wu
	JOIN Experiments e ON wu.BitReversedXid = e.BitReversedXid
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

// WorkUnitWithAuthor wraps a WorkUnit proto with author metadata.
type WorkUnitWithAuthor struct {
	WorkUnit *essmsgpb.WorkUnit
	Author   string
}

// ActiveWorkUnitsResult holds the result of querying active work units.
type ActiveWorkUnitsResult struct {
	WorkUnits      []WorkUnitWithAuthor
	NextPageToken  string
	TotalQueueSize int64
}

type workUnitPK struct {
	BitReversedXid int64
	WorkUnitId     int64
	Creator        string
}

func decodePageToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	bytes, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		bytes, err = base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidPageToken, err)
		}
	}
	offset, err := strconv.ParseInt(string(bytes), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPageToken, err)
	}
	if offset < 0 {
		return 0, fmt.Errorf("%w: offset cannot be negative", ErrInvalidPageToken)
	}
	return offset, nil
}

func encodePageToken(offset int64) string {
	return base64.URLEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
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
		var creator string
		if err := row.ColumnByName("BitReversedXid", &bitReversedXid); err != nil {
			return fmt.Errorf("failed to read column BitReversedXid: %w", err)
		}
		if err := row.ColumnByName("WorkUnitId", &workUnitId); err != nil {
			return fmt.Errorf("failed to read column WorkUnitId: %w", err)
		}
		if err := row.ColumnByName("Creator", &creator); err != nil {
			return fmt.Errorf("failed to read column Creator: %w", err)
		}
		pk := workUnitPK{
			BitReversedXid: bitReversedXid,
			WorkUnitId:     workUnitId,
			Creator:        creator,
		}
		orderedPKs = append(orderedPKs, pk)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to query active work unit primary keys: %w", err)
	}

	return orderedPKs, nil
}

func batchReadWorkUnits(ctx context.Context, txn *spanner.ReadOnlyTransaction, keySetList []spanner.KeySet, orderedPKs []workUnitPK) ([]WorkUnitWithAuthor, error) {
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

	workUnits := make([]WorkUnitWithAuthor, 0, len(orderedPKs))
	for _, pk := range orderedPKs {
		key := workUnitKey{
			bitReversedXid: pk.BitReversedXid,
			workUnitId:     pk.WorkUnitId,
		}
		wu, ok := workUnitMap[key]
		if !ok {
			return nil, fmt.Errorf("work unit with PK (BitReversedXid: %d, WorkUnitId: %d) missing from batch read result", pk.BitReversedXid, pk.WorkUnitId)
		}
		workUnits = append(workUnits, WorkUnitWithAuthor{
			WorkUnit: wu,
			Author:   pk.Creator,
		})
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
			WorkUnits:     []WorkUnitWithAuthor{},
			NextPageToken: "",
		}, nil
	}

	pageSizeVal := pageSize
	if pageSizeVal <= 0 {
		pageSizeVal = defaultPageSize
	}
	pageSizeVal = min(pageSizeVal, maxPageSize)

	offset, err := decodePageToken(pageToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w", err)
	}

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	// Query 1: Count total active work units across matching queues
	totalQueueSize, err := countActiveWorkUnits(ctx, txn, queueNames)
	if err != nil {
		logging.Error(ctx, "failed to count active work units", "error", err)
		return nil, err
	}

	// Fetch pageSize + 1 items to determine if nextPageToken is needed
	limit := int64(pageSizeVal) + 1
	orderedPKs, err := fetchActiveWorkUnitPKs(ctx, txn, queueNames, limit, offset)
	if err != nil {
		logging.Error(ctx, "failed to fetch active work unit primary keys", "error", err)
		return nil, err
	}

	if len(orderedPKs) == 0 {
		return &ActiveWorkUnitsResult{
			WorkUnits:      []WorkUnitWithAuthor{},
			NextPageToken:  "",
			TotalQueueSize: totalQueueSize,
		}, nil
	}

	nextPageToken := ""
	if int64(len(orderedPKs)) > int64(pageSizeVal) {
		orderedPKs = orderedPKs[:pageSizeVal]
		nextPageToken = encodePageToken(offset + int64(pageSizeVal))
	}

	keys := make([]spanner.KeySet, len(orderedPKs))
	for i, pk := range orderedPKs {
		keys[i] = spanner.Key{pk.BitReversedXid, pk.WorkUnitId}
	}
	keySetList := []spanner.KeySet{spanner.KeySets(keys...)}

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
