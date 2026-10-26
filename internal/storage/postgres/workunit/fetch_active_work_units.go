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
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

const (
	// defaultPageSize is the page size of FetchActiveWorkUnitsByQueues when none is requested.
	defaultPageSize = int32(100)
	// maxPageSize caps the page size of FetchActiveWorkUnitsByQueues.
	maxPageSize = int32(1000)
)

// fetchActiveWorkUnitsSQL reads a page of the pending and running work units of the requested
// queues together with the creator of their experiments, in the order of the
// WorkUnitsByQueueAndPosition index: running work units before scheduling ones, then by queue
// position, creation time and work unit ID. The experiment ID breaks the remaining ties so that
// offset pages never overlap.
var fetchActiveWorkUnitsSQL = safesql.New("SELECT " + workUnitColumns + `, COALESCE(e.Creator, '') AS Creator
FROM WorkUnits wu
JOIN Experiments e ON wu.Xid = e.Xid
WHERE wu.QueueName = ANY($1)
  AND wu.IsPendingOrRunningJob IS NOT NULL
ORDER BY wu.SortPhase DESC, wu.PositionInQueue ASC, wu.CreateTime ASC, wu.WorkUnitId ASC, wu.Xid ASC
LIMIT $2 OFFSET $3`)

// countActiveWorkUnitsSQL counts the pending and running work units of the requested queues.
var countActiveWorkUnitsSQL = safesql.New(`SELECT COUNT(*)
FROM WorkUnits wu
WHERE wu.QueueName = ANY($1)
  AND wu.IsPendingOrRunningJob IS NOT NULL`)

// activeWorkUnitRow is one row of fetchActiveWorkUnitsSQL.
type activeWorkUnitRow struct {
	workUnitRow
	Creator string
}

// decodePageToken decodes an offset page token of FetchActiveWorkUnitsByQueues; the empty token
// is offset 0.
func decodePageToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	bytes, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		bytes, err = base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", storage.ErrInvalidPageToken, err)
		}
	}
	offset, err := strconv.ParseInt(string(bytes), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", storage.ErrInvalidPageToken, err)
	}
	if offset < 0 {
		return 0, fmt.Errorf("%w: offset cannot be negative", storage.ErrInvalidPageToken)
	}
	return offset, nil
}

// encodePageToken encodes offset as a page token of FetchActiveWorkUnitsByQueues.
func encodePageToken(offset int64) string {
	return base64.URLEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}

// FetchActiveWorkUnitsByQueues retrieves a page of the pending and running work units of the
// requested queues, each with the creator of its experiment as author, together with the total
// number of such work units. Pages are addressed by offset tokens; pageSize defaults to
// defaultPageSize and is capped at maxPageSize. The count and the page are read from one database
// snapshot.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.pool == nil {
		return nil, errNilPool
	}
	if len(queueNames) == 0 {
		return &storage.ActiveWorkUnitsResult{
			WorkUnits:     []storage.WorkUnitWithAuthor{},
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

	result := &storage.ActiveWorkUnitsResult{}
	readErr := r.readSnapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Query 1: Count total active work units across matching queues.
		totalQueueSize, err := countActiveWorkUnits(ctx, tx, queueNames)
		if err != nil {
			logging.Error(ctx, "failed to count active work units", "error", err)
			return err
		}

		// Query 2: Fetch pageSize + 1 items to determine if nextPageToken is needed.
		workUnits, err := fetchActiveWorkUnits(ctx, tx, queueNames, int64(pageSizeVal)+1, offset)
		if err != nil {
			logging.Error(ctx, "failed to fetch active work units", "error", err)
			return err
		}

		nextPageToken := ""
		if len(workUnits) > int(pageSizeVal) {
			workUnits = workUnits[:pageSizeVal]
			nextPageToken = encodePageToken(offset + int64(pageSizeVal))
		}
		*result = storage.ActiveWorkUnitsResult{
			WorkUnits:      workUnits,
			NextPageToken:  nextPageToken,
			TotalQueueSize: totalQueueSize,
		}
		return nil
	})
	if readErr != nil {
		return nil, readErr
	}
	return result, nil
}

// countActiveWorkUnits counts the pending and running work units of queueNames on db.
func countActiveWorkUnits(ctx context.Context, db pgdb.DBTX, queueNames []string) (int64, error) {
	var totalQueueSize int64
	if err := db.QueryRow(ctx, countActiveWorkUnitsSQL.String(), queueNames).Scan(&totalQueueSize); err != nil {
		return 0, fmt.Errorf("failed to count active work units: %w", pgdb.TranslateError(err, workUnitEntity))
	}
	return totalQueueSize, nil
}

// fetchActiveWorkUnits reads limit pending and running work units of queueNames, starting at
// offset in queue order, on db.
func fetchActiveWorkUnits(ctx context.Context, db pgdb.DBTX, queueNames []string, limit, offset int64) ([]storage.WorkUnitWithAuthor, error) {
	rows, err := db.Query(ctx, fetchActiveWorkUnitsSQL.String(), queueNames, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query active work units: %w", pgdb.TranslateError(err, workUnitEntity))
	}
	activeRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[activeWorkUnitRow])
	if err != nil {
		return nil, fmt.Errorf("failed to query active work units: %w", pgdb.TranslateError(err, workUnitEntity))
	}
	workUnits := make([]storage.WorkUnitWithAuthor, 0, len(activeRows))
	for _, row := range activeRows {
		workUnit, err := row.workUnitRow.toProto()
		if err != nil {
			return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
		}
		workUnits = append(workUnits, storage.WorkUnitWithAuthor{WorkUnit: workUnit, Author: row.Creator})
	}
	return workUnits, nil
}
