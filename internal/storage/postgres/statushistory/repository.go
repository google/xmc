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

// Package statushistory contains the concrete PostgreSQL repository implementation for status history and velocity metrics.
package statushistory

import (
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

const entityName = "work unit status history"

var (
	insertHistorySQL = safesql.New(`INSERT INTO WorkUnitStatusHistory (
  Xid, WorkUnitId, HistoryId, ExecutionId, QueueName, PreviousState, NewState, ResourceType, ResourceUnits, CreateTime
) VALUES (
  @xid, @workunitid, @historyid, @executionid, @queuename, @previousstate, @newstate, @resourcetype, @resourceunits, @createtime
)`)

	// The queue metric queries below mirror FetchBatchQueueVelocity and FetchBatchPendingQueueMetrics in
	// internal/storage/spanner/statushistory/statushistory.go. Keep them in sync; the parameterized tests in
	// both packages share the same cases and expected rows.
	selectBatchQueueVelocitySQL = safesql.New(`WITH Admitted AS (
  SELECT QueueName, Xid, WorkUnitId, ResourceType, ResourceUnits
  FROM WorkUnitStatusHistory
  WHERE QueueName = ANY($1)
    AND CreateTime >= $2
    AND NewState = ANY($3)
),
QueueTotals AS (
  SELECT QueueName, COUNT(DISTINCT (Xid, WorkUnitId)) AS TotalAdmittedJobs
  FROM Admitted
  GROUP BY QueueName
)
SELECT a.QueueName AS QueueName,
       qt.TotalAdmittedJobs AS Count,
       COALESCE(SUM(a.ResourceUnits), 0)::BIGINT AS TotalUnits,
       COALESCE(a.ResourceType, '') AS ResourceType
FROM Admitted a
JOIN QueueTotals qt ON a.QueueName = qt.QueueName
GROUP BY a.QueueName, qt.TotalAdmittedJobs, a.ResourceType`)

	selectBatchPendingQueueMetricsSQL = safesql.New(`WITH MaxHistory AS (
  SELECT Xid, WorkUnitId, MAX(CreateTime) AS MaxCreateTime
  FROM WorkUnitStatusHistory
  WHERE QueueName = ANY($1)
  GROUP BY Xid, WorkUnitId
),
LatestPending AS (
  SELECT h.QueueName, h.Xid, h.WorkUnitId, h.ResourceType, h.ResourceUnits
  FROM WorkUnitStatusHistory h
  JOIN MaxHistory m
    ON h.Xid = m.Xid
   AND h.WorkUnitId = m.WorkUnitId
   AND h.CreateTime = m.MaxCreateTime
  WHERE h.NewState = $2
),
QueueTotals AS (
  SELECT QueueName, COUNT(DISTINCT (Xid, WorkUnitId)) AS TotalPendingJobs
  FROM LatestPending
  GROUP BY QueueName
)
SELECT lp.QueueName AS QueueName,
       qt.TotalPendingJobs AS Count,
       COALESCE(SUM(lp.ResourceUnits), 0)::BIGINT AS TotalUnits,
       COALESCE(lp.ResourceType, '') AS ResourceType
FROM LatestPending lp
JOIN QueueTotals qt ON lp.QueueName = qt.QueueName
GROUP BY lp.QueueName, qt.TotalPendingJobs, lp.ResourceType`)

	admittedStates = []int64{
		int64(essmsgpb.WorkUnitStatus_STARTING),
		int64(essmsgpb.WorkUnitStatus_RUNNING),
	}
)

var (
	_ storage.StatusHistoryStore        = (*Repository)(nil)
	_ storage.StatusHistoryTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for status history and velocity metrics in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository manages transactional persistence for status history in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL StatusHistory repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// FetchBatchQueueVelocity queries the status history table for multiple queues and returns raw metric rows.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]storage.QueueMetricRow, error) {
	if len(queueNames) == 0 || r.db == nil {
		return nil, nil
	}
	cutoff := time.Now().UTC().Add(-window)
	return r.queryQueueMetrics(ctx, selectBatchQueueVelocitySQL, queueNames, cutoff, admittedStates)
}

// FetchBatchPendingQueueMetrics queries the status history table for pending work units across multiple queues.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]storage.QueueMetricRow, error) {
	if len(queueNames) == 0 || r.db == nil {
		return nil, nil
	}
	return r.queryQueueMetrics(ctx, selectBatchPendingQueueMetricsSQL, queueNames, int64(essmsgpb.WorkUnitStatus_SCHEDULING))
}

// SaveWorkUnitStatusHistory records work unit status history entries within the transaction.
func (r *TxRepository) SaveWorkUnitStatusHistory(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	if err := r.validateSaveContext(ctx); err != nil {
		return err
	}
	for _, resType := range slices.Sorted(maps.Keys(resources)) {
		row, err := postgresdata.NewWorkUnitStatusHistoryRow(
			xid, wid, generateHistoryID(), eid, queueName, prevState, newState, resType, resources[resType], timestamp,
		)
		if err != nil {
			return pgdb.WrapStatusError(fmt.Sprintf("failed to build work unit status history row: %v", err), grpcerr.ErrInvalidArgument, err)
		}
		if _, err := r.tx.Exec(ctx, insertHistorySQL.String(), row.GetInsertArgs()); err != nil {
			return pgdb.TranslateError(err, entityName)
		}
	}
	return nil
}

func (r *Repository) queryQueueMetrics(ctx context.Context, query safesql.TrustedSQLString, args ...any) ([]storage.QueueMetricRow, error) {
	return pgdb.QueryRows[storage.QueueMetricRow](ctx, r.db, query.String(), entityName, args...)
}

func (r *TxRepository) validateSaveContext(ctx context.Context) error {
	if ctx == nil {
		return pgdb.WrapStatusError("context must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.tx == nil {
		return pgdb.WrapStatusError("transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return nil
}

func generateHistoryID() int64 {
	return rand.Int64N(math.MaxInt64-1) + 1
}
