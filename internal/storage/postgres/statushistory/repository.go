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
	"math"
	mrand "math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// entityName names work unit status history rows in translated storage errors.
const entityName = "work unit status history"

var (
	_ storage.StatusHistoryStore        = (*Repository)(nil)
	_ storage.StatusHistoryTxRepository = (*TxRepository)(nil)
)

// insertHistorySQL appends one state transition row for one resource type.
var insertHistorySQL = safesql.New(`INSERT INTO WorkUnitStatusHistory (Xid, WorkUnitId, HistoryId, ExecutionId, QueueName, PreviousState, NewState, ResourceType, ResourceUnits, CreateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`)

// queueVelocitySQL counts, per queue and resource type, the distinct work units admitted (a
// transition into STARTING or RUNNING) since the cutoff time and sums their resource units.
// AdmittedCount is the per-queue total over all resource types. It mirrors the Spanner query, which
// forces the WorkUnitStatusHistoryByQueueAndCreateTime index; PostgreSQL's planner is left to pick it.
var queueVelocitySQL = safesql.New(`WITH Admitted AS (
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
SELECT a.QueueName,
       qt.TotalAdmittedJobs AS AdmittedCount,
       COALESCE(SUM(a.ResourceUnits), 0)::BIGINT AS TotalUnits,
       COALESCE(a.ResourceType, '') AS ResType
FROM Admitted a
JOIN QueueTotals qt ON a.QueueName = qt.QueueName
GROUP BY a.QueueName, qt.TotalAdmittedJobs, a.ResourceType`)

// pendingQueueMetricsSQL counts, per queue and resource type, the distinct work units whose latest
// transition (within the requested queues) is into SCHEDULING and sums their resource units.
// PendingJobs is the per-queue total over all resource types. As in the Spanner query, the latest
// transition is found among the requested queues' rows, and the rows at that time are then read
// without a queue filter.
var pendingQueueMetricsSQL = safesql.New(`WITH MaxHistory AS (
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
SELECT lp.QueueName,
       qt.TotalPendingJobs AS PendingJobs,
       COALESCE(SUM(lp.ResourceUnits), 0)::BIGINT AS TotalUnits,
       COALESCE(lp.ResourceType, '') AS ResType
FROM LatestPending lp
JOIN QueueTotals qt ON lp.QueueName = qt.QueueName
GROUP BY lp.QueueName, qt.TotalPendingJobs, lp.ResourceType`)

// admittedStates are the transitions that count as a queue admitting a work unit.
var admittedStates = []int64{
	int64(essmsgpb.WorkUnitStatus_STARTING),
	int64(essmsgpb.WorkUnitStatus_RUNNING),
}

// pendingState is the state of a work unit waiting in a queue.
const pendingState = int64(essmsgpb.WorkUnitStatus_SCHEDULING)

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

// SaveWorkUnitStatusHistory inserts one WorkUnitStatusHistory row per entry of resources for the
// transition of work unit (xid, wid) from prevState to newState, all stamped with timestamp (or the
// current time when it is zero). A nil prevState is stored as NULL; a nil newState is an error
// unless resources is empty, in which case nothing is written.
func (r *TxRepository) SaveWorkUnitStatusHistory(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	for resType, units := range resources {
		args, err := historyInsertArgs(xid, wid, generateHistoryID(), eid, queueName, prevState, newState, resType, units, timestamp)
		if err != nil {
			return fmt.Errorf("failed to create work unit status history insert: %w", err)
		}
		if _, err = r.tx.Exec(ctx, insertHistorySQL.String(), args...); err != nil {
			return pgdb.TranslateError(err, entityName)
		}
	}
	return nil
}

// generateHistoryID draws a random HistoryId in [1, math.MaxInt64-1], as the Spanner backend does.
func generateHistoryID() int64 {
	return mrand.Int63n(math.MaxInt64-1) + 1
}

// historyInsertArgs builds the positional arguments of insertHistorySQL for one transition row.
func historyInsertArgs(xid, wid, historyID int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resourceType string, resourceUnits int64, t time.Time) ([]any, error) {
	if newState == nil {
		return nil, fmt.Errorf("newState cannot be nil")
	}
	var previousState *int64
	if prevState != nil {
		previousState = new(int64)
		*previousState = int64(*prevState)
	}
	if t.IsZero() {
		t = time.Now()
	}
	return []any{
		xid,              // Xid
		wid,              // WorkUnitId
		historyID,        // HistoryId
		eid,              // ExecutionId
		queueName,        // QueueName
		previousState,    // PreviousState
		int64(*newState), // NewState
		resourceType,     // ResourceType
		resourceUnits,    // ResourceUnits
		t,                // CreateTime
	}, nil
}

// FetchBatchQueueVelocity returns, for each of queueNames and each resource type, how many distinct
// work units the queue admitted within the last window and how many resource units they requested.
// It returns nil for no queue names or an unbound repository.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]storage.QueueMetricRow, error) {
	if len(queueNames) == 0 || r.db == nil {
		return nil, nil
	}
	cutoff := time.Now().Add(-window)
	return r.runQueueMetricQuery(ctx, queueVelocitySQL, queueNames, cutoff, admittedStates)
}

// FetchBatchPendingQueueMetrics returns, for each of queueNames and each resource type, how many
// distinct work units are currently pending (their latest transition is into SCHEDULING) and how
// many resource units they requested. It returns nil for no queue names or an unbound repository.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]storage.QueueMetricRow, error) {
	if len(queueNames) == 0 || r.db == nil {
		return nil, nil
	}
	return r.runQueueMetricQuery(ctx, pendingQueueMetricsSQL, queueNames, pendingState)
}

// runQueueMetricQuery runs one of the queue metric queries, whose result columns are QueueName, the
// per-queue count, TotalUnits and ResType in that order.
func (r *Repository) runQueueMetricQuery(ctx context.Context, query safesql.TrustedSQLString, args ...any) ([]storage.QueueMetricRow, error) {
	rows, err := r.db.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query queue metrics: %w", pgdb.TranslateError(err, entityName))
	}
	metrics, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (storage.QueueMetricRow, error) {
		var m storage.QueueMetricRow
		scanErr := row.Scan(&m.QueueName, &m.Count, &m.TotalUnits, &m.ResourceType)
		return m, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate queue metric rows: %w", pgdb.TranslateError(err, entityName))
	}
	if len(metrics) == 0 {
		return nil, nil
	}
	return metrics, nil
}
