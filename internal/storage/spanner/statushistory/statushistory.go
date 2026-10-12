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

package statushistory

import (
	"context"
	"fmt"
	"math"
	mrand "math/rand"
	"time"

	"cloud.google.com/go/spanner"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/common/resourceaccumulator"
	"github.com/google/xmc/internal/kubernetes/common/constants"
	spannerdata "github.com/google/xmc/internal/storage/spanner/data"
	"google.golang.org/api/iterator"
)

// WorkUnitStatusHistoryItem represents a single WorkUnitStatusHistory record for Spanner queries and UNNEST mocks.
type WorkUnitStatusHistoryItem struct {
	BitReversedXid int64     `spanner:"BitReversedXid"`
	WorkUnitId     int64     `spanner:"WorkUnitId"`
	HistoryId      int64     `spanner:"HistoryId"`
	QueueName      string    `spanner:"QueueName"`
	CreateTime     time.Time `spanner:"CreateTime"`
	NewState       int64     `spanner:"NewState"`
	PreviousState  int64     `spanner:"PreviousState"`
	ResourceType   string    `spanner:"ResourceType"`
	ResourceUnits  int64     `spanner:"ResourceUnits"`
}

// ResourceTypeCPU represents the standard CPU resource identifier string.
const ResourceTypeCPU = constants.ResourceTypeCPU

// QueueMetricRow represents a single raw row returned by the Spanner velocity or pending metrics query.
type QueueMetricRow struct {
	QueueName    string
	Count        int64
	TotalUnits   int64
	ResourceType string
}

// ExtractAllResourcesFromExecutable extracts all compute resource types and unit counts from an Executable.
func ExtractAllResourcesFromExecutable(exec *essmsgpb.Executable) map[string]int64 {
	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(exec)
	return acc.IntTotals()
}

// SaveWorkUnitStatusHistoryIfChanged buffers Spanner mutations for all requested resources if the WorkUnit state transitioned.
// TODO(b/517479143): Inline into Repository.SaveWorkUnitStatusHistoryIfChanged once CreateWorkUnit and UpdateWorkUnitExecutions are moved out of Spanner.
func SaveWorkUnitStatusHistoryIfChanged(ctx context.Context, xid, wid int64, eid *int64, queueName string, currentState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resourceMap map[string]int64, t time.Time, tx *spanner.ReadWriteTransaction) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	if newState == nil {
		return
	}
	if currentState != nil && *currentState == *newState {
		return
	}

	if len(resourceMap) == 0 {
		resourceMap = map[string]int64{"": 0}
	}

	if t.IsZero() {
		t = time.Now()
	}
	for resType, units := range resourceMap {
		historyID := generateHistoryID()
		mutation, err := spannerdata.WorkUnitStatusHistoryToInsertMutation(xid, wid, historyID, eid, queueName, currentState, newState, resType, units, t)
		if err != nil {
			continue
		}
		tx.BufferWrite([]*spanner.Mutation{mutation})
	}
}

func generateHistoryID() int64 {
	return mrand.Int63n(math.MaxInt64-1) + 1
}

// Repository handles Spanner operations for status history and velocity metrics.
type Repository struct {
	client *spanner.Client
}

// TxRepository manages transactional persistence for status history in Cloud Spanner.
type TxRepository struct {
	*Repository
	tx *spanner.ReadWriteTransaction
}

// NewRepository initializes a new statushistory Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// WithTx returns a wrapper bound to the given read-write transaction.
func (r *Repository) WithTx(tx *spanner.ReadWriteTransaction) *TxRepository {
	return &TxRepository{
		Repository: r,
		tx:         tx,
	}
}

// SaveWorkUnitStatusHistoryIfChanged buffers a work unit status history mutation if the state changed using the bound read-write transaction.
// TODO(b/517479143): Mutation build/buffer failures are currently logged and skipped by the
// delegated helper; return them once it is inlined here.
func (r *TxRepository) SaveWorkUnitStatusHistoryIfChanged(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	SaveWorkUnitStatusHistoryIfChanged(ctx, xid, wid, eid, queueName, prevState, newState, resources, timestamp, r.tx)
	return nil
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues and returns raw rows.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]QueueMetricRow, error) {
	return FetchBatchQueueVelocity(ctx, r.client, queueNames, window)
}

// FetchBatchPendingQueueMetrics queries the WorkUnitStatusHistory table for pending work units and returns raw rows.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]QueueMetricRow, error) {
	return FetchBatchPendingQueueMetrics(ctx, r.client, queueNames)
}

func runQueueMetricQuery(ctx context.Context, client *spanner.Client, stmt spanner.Statement, countCol string) ([]QueueMetricRow, error) {
	if client == nil {
		return nil, nil
	}

	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var rows []QueueMetricRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate queue metric rows: %w", err)
		}

		var row QueueMetricRow
		if err := r.ColumnByName("QueueName", &row.QueueName); err != nil {
			return nil, fmt.Errorf("failed to read QueueName column: %w", err)
		}
		if err := r.ColumnByName(countCol, &row.Count); err != nil {
			return nil, fmt.Errorf("failed to read %s column: %w", countCol, err)
		}
		if err := r.ColumnByName("TotalUnits", &row.TotalUnits); err != nil {
			return nil, fmt.Errorf("failed to read TotalUnits column: %w", err)
		}
		if err := r.ColumnByName("ResType", &row.ResourceType); err != nil {
			return nil, fmt.Errorf("failed to read ResType column: %w", err)
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues in a single Spanner query.
func FetchBatchQueueVelocity(ctx context.Context, client *spanner.Client, queueNames []string, window time.Duration) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 || client == nil {
		return nil, nil
	}

	cutoff := time.Now().Add(-window)
	stmt := spanner.Statement{
		SQL: `WITH Admitted AS (
		        SELECT QueueName, BitReversedXid, WorkUnitId, ResourceType, ResourceUnits
		        FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndCreateTime}
		        WHERE QueueName IN UNNEST(@queueNames)
		          AND CreateTime >= @cutoffTime
		          AND CAST(NewState AS INT64) IN UNNEST(@admittedStates)
		      ),
		      QueueTotals AS (
		        SELECT QueueName, COUNT(DISTINCT CONCAT(CAST(BitReversedXid AS STRING), '-', CAST(WorkUnitId AS STRING))) AS TotalAdmittedJobs
		        FROM Admitted
		        GROUP BY QueueName
		      )
		      SELECT a.QueueName,
		             qt.TotalAdmittedJobs AS AdmittedCount,
		             IFNULL(SUM(a.ResourceUnits), 0) AS TotalUnits,
		             IFNULL(a.ResourceType, '') AS ResType
		      FROM Admitted a
		      JOIN QueueTotals qt ON a.QueueName = qt.QueueName
		      GROUP BY a.QueueName, qt.TotalAdmittedJobs, a.ResourceType`,
		Params: map[string]interface{}{
			"queueNames": queueNames,
			"cutoffTime": cutoff,
			"admittedStates": []int64{
				int64(essmsgpb.WorkUnitStatus_STARTING),
				int64(essmsgpb.WorkUnitStatus_RUNNING),
			},
		},
	}

	return runQueueMetricQuery(ctx, client, stmt, "AdmittedCount")
}

// FetchBatchPendingQueueMetrics queries the WorkUnitStatusHistory table for pending work units across multiple queues.
func FetchBatchPendingQueueMetrics(ctx context.Context, client *spanner.Client, queueNames []string) ([]QueueMetricRow, error) {
	if len(queueNames) == 0 || client == nil {
		return nil, nil
	}

	stmt := spanner.Statement{
		SQL: `WITH MaxHistory AS (
		        SELECT BitReversedXid, WorkUnitId, MAX(CreateTime) AS MaxCreateTime
		        FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndCreateTime}
		        WHERE QueueName IN UNNEST(@queueNames)
		        GROUP BY BitReversedXid, WorkUnitId
		      ),
		      LatestPending AS (
		        SELECT h.QueueName, h.BitReversedXid, h.WorkUnitId, h.ResourceType, h.ResourceUnits
		        FROM WorkUnitStatusHistory h
		        JOIN MaxHistory m
		          ON h.BitReversedXid = m.BitReversedXid
		         AND h.WorkUnitId = m.WorkUnitId
		         AND h.CreateTime = m.MaxCreateTime
		        WHERE CAST(h.NewState AS INT64) = @pendingState
		      ),
		      QueueTotals AS (
		        SELECT QueueName, COUNT(DISTINCT CONCAT(CAST(BitReversedXid AS STRING), '-', CAST(WorkUnitId AS STRING))) AS TotalPendingJobs
		        FROM LatestPending
		        GROUP BY QueueName
		      )
		      SELECT lp.QueueName,
		             qt.TotalPendingJobs AS PendingJobs,
		             IFNULL(SUM(lp.ResourceUnits), 0) AS TotalUnits,
		             IFNULL(lp.ResourceType, '') AS ResType
		      FROM LatestPending lp
		      JOIN QueueTotals qt ON lp.QueueName = qt.QueueName
		      GROUP BY lp.QueueName, qt.TotalPendingJobs, lp.ResourceType`,
		Params: map[string]interface{}{
			"queueNames":   queueNames,
			"pendingState": int64(essmsgpb.WorkUnitStatus_SCHEDULING),
		},
	}

	return runQueueMetricQuery(ctx, client, stmt, "PendingJobs")
}
