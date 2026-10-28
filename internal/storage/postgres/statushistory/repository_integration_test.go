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

package statushistory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const resourceTypeCPU = "CPU"

// historyItem describes one WorkUnitStatusHistory transition row to seed a test with.
type historyItem struct {
	Xid           int64
	WorkUnitID    int64
	QueueName     string
	CreateTime    time.Time
	PreviousState *essmsgpb.WorkUnitStatus_WorkUnitState
	NewState      essmsgpb.WorkUnitStatus_WorkUnitState
	ResourceType  string
	ResourceUnits int64
}

// storedHistory is a WorkUnitStatusHistory row read back with raw SQL, bypassing the repository.
type storedHistory struct {
	Xid           int64
	WorkUnitID    int64
	HistoryID     int64
	ExecutionID   *int64
	QueueName     *string
	PreviousState *int64
	NewState      int64
	ResourceType  *string
	ResourceUnits *int64
	CreateTime    time.Time
}

// fixture is a Repository backed by a fresh ESS test database, together with the database's pool
// for raw SQL and for running transactions.
type fixture struct {
	repo *Repository
	pool *pgxpool.Pool
}

// newFixture creates a fresh ESS test database and a Repository bound to it.
func newFixture(t *testing.T) fixture {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return fixture{repo: NewRepository(essPool), pool: essPool}
}

// save runs SaveWorkUnitStatusHistory in a transaction of its own.
func (f fixture) save(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	return pgdb.RunInTx(ctx, f.pool, func(ctx context.Context, tx pgx.Tx) error {
		return f.repo.WithTx(tx).SaveWorkUnitStatusHistory(ctx, xid, wid, eid, queueName, prevState, newState, resources, timestamp)
	})
}

// insertItems seeds items through SaveWorkUnitStatusHistory, one transaction per item, with the
// item's index as its execution ID.
func (f fixture) insertItems(ctx context.Context, t *testing.T, items []historyItem) {
	t.Helper()
	for i, item := range items {
		eid := int64(i + 1)
		resources := map[string]int64{item.ResourceType: item.ResourceUnits}
		if err := f.save(ctx, item.Xid, item.WorkUnitID, &eid, item.QueueName, item.PreviousState, &item.NewState, resources, item.CreateTime); err != nil {
			t.Fatalf("SaveWorkUnitStatusHistory(item %d) unexpected error: %v", i, err)
		}
	}
}

// queryStoredHistory reads the WorkUnitStatusHistory rows of work unit (xid, wid) with raw SQL,
// ordered by resource type.
func queryStoredHistory(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid int64) []storedHistory {
	t.Helper()
	const query = `SELECT Xid, WorkUnitId, HistoryId, ExecutionId, QueueName, PreviousState, NewState, ResourceType, ResourceUnits, CreateTime
FROM WorkUnitStatusHistory WHERE Xid = $1 AND WorkUnitId = $2 ORDER BY ResourceType`
	rows, err := pool.Query(ctx, query, xid, wid)
	if err != nil {
		t.Fatalf("Query(WorkUnitStatusHistory) unexpected error: %v", err)
	}
	stored, err := pgx.CollectRows(rows, pgx.RowToStructByPos[storedHistory])
	if err != nil {
		t.Fatalf("CollectRows(WorkUnitStatusHistory) unexpected error: %v", err)
	}
	return stored
}

// countHistoryRows counts all WorkUnitStatusHistory rows with raw SQL.
func countHistoryRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM WorkUnitStatusHistory").Scan(&count); err != nil {
		t.Fatalf("QueryRow(COUNT) unexpected error: %v", err)
	}
	return count
}

// newItem builds a transition of work unit (1, wid) in queue at now+offset.
func newItem(wid int64, queue string, offset time.Duration, state essmsgpb.WorkUnitStatus_WorkUnitState, resType string, units int64) historyItem {
	return historyItem{
		Xid:           1,
		WorkUnitID:    wid,
		QueueName:     queue,
		CreateTime:    time.Now().Add(offset),
		NewState:      state,
		ResourceType:  resType,
		ResourceUnits: units,
	}
}

func sortQueueMetricRows(rows []storage.QueueMetricRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].QueueName != rows[j].QueueName {
			return rows[i].QueueName < rows[j].QueueName
		}
		return rows[i].ResourceType < rows[j].ResourceType
	})
}

func compareQueueMetricRows(t *testing.T, got, want []storage.QueueMetricRow) {
	t.Helper()
	sortQueueMetricRows(got)
	sortQueueMetricRows(want)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("queue metric rows mismatch (-want +got):\n%s", diff)
	}
}

func TestFetchBatchQueueVelocity_Parameterized(t *testing.T) {
	tests := []struct {
		name       string
		queueNames []string
		window     time.Duration
		items      []historyItem
		want       []storage.QueueMetricRow
	}{
		{
			name:       "TransitionStateFiltering",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 20),
				newItem(103, "q1", -2*time.Minute, essmsgpb.WorkUnitStatus_INITIALIZING, resourceTypeCPU, 30),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "LookbackCutoffWindowFiltering",
			queueNames: []string{"q1"},
			window:     30 * time.Minute,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(102, "q1", -45*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 20),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 20),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(102, "q1", -8*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 20),
				newItem(103, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, "GPU", 4),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 3, TotalUnits: 30, ResourceType: resourceTypeCPU},
				{QueueName: "q1", Count: 3, TotalUnits: 4, ResourceType: "GPU"},
			},
		},
		{
			name:       "MultipleQueues",
			queueNames: []string{"q1", "q2"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, resourceTypeCPU, 20),
				newItem(202, "q2", -9*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, "GPU", 2),
				newItem(301, "q3", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, resourceTypeCPU, 40),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
				{QueueName: "q2", Count: 2, TotalUnits: 20, ResourceType: resourceTypeCPU},
				{QueueName: "q2", Count: 2, TotalUnits: 2, ResourceType: "GPU"},
			},
		},
		{
			// A work unit admitted twice in the window is one distinct job, while its resource
			// units are summed over both transitions, as in the Spanner query.
			name:       "RepeatedAdmissionCountsOnce",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
				newItem(101, "q1", -9*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, resourceTypeCPU, 10),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 20, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "NoMatchingRows",
			queueNames: []string{"q1"},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_COMPLETED, resourceTypeCPU, 10),
			},
			want: nil,
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			window:     1 * time.Hour,
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 10),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t)
			f.insertItems(ctx, t, tt.items)

			got, err := f.repo.FetchBatchQueueVelocity(ctx, tt.queueNames, tt.window)
			if err != nil {
				t.Fatalf("FetchBatchQueueVelocity() unexpected error: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}

func TestFetchBatchPendingQueueMetrics_Parameterized(t *testing.T) {
	tests := []struct {
		name       string
		queueNames []string
		items      []historyItem
		want       []storage.QueueMetricRow
	}{
		{
			name:       "TransitionStateFiltering",
			queueNames: []string{"q1"},
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_STARTING, resourceTypeCPU, 20),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "LatestStateTransition",
			queueNames: []string{"q1"},
			items: []historyItem{
				newItem(101, "q1", -20*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, resourceTypeCPU, 10),
			},
			want: nil,
		},
		{
			name:       "ReturnsToPending",
			queueNames: []string{"q1"},
			items: []historyItem{
				newItem(101, "q1", -30*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(101, "q1", -20*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, resourceTypeCPU, 10),
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "QueueNameFiltering",
			queueNames: []string{"q1"},
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 20),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "MultiResourceAggregation",
			queueNames: []string{"q1"},
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(102, "q1", -8*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 20),
				newItem(103, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, "GPU", 4),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 3, TotalUnits: 30, ResourceType: resourceTypeCPU},
				{QueueName: "q1", Count: 3, TotalUnits: 4, ResourceType: "GPU"},
			},
		},
		{
			name:       "MultipleQueues",
			queueNames: []string{"q1", "q2"},
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
				newItem(201, "q2", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 20),
				newItem(202, "q2", -9*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 30),
				newItem(301, "q3", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 40),
			},
			want: []storage.QueueMetricRow{
				{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: resourceTypeCPU},
				{QueueName: "q2", Count: 2, TotalUnits: 50, ResourceType: resourceTypeCPU},
			},
		},
		{
			name:       "EmptyQueueNames",
			queueNames: []string{},
			items: []historyItem{
				newItem(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, resourceTypeCPU, 10),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t)
			f.insertItems(ctx, t, tt.items)

			got, err := f.repo.FetchBatchPendingQueueMetrics(ctx, tt.queueNames)
			if err != nil {
				t.Fatalf("FetchBatchPendingQueueMetrics() unexpected error: %v", err)
			}
			compareQueueMetricRows(t, got, tt.want)
		})
	}
}

// A single transition with several resource types is one pending job per resource type, with the
// per-queue job count shared across its rows.
func TestFetchBatchPendingQueueMetrics_MultiResourceTransition(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)

	scheduling := essmsgpb.WorkUnitStatus_SCHEDULING
	resources := map[string]int64{resourceTypeCPU: 8, "GPU": 2}
	if err := f.save(ctx, 1, 101, nil, "q1", nil, &scheduling, resources, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("SaveWorkUnitStatusHistory() unexpected error: %v", err)
	}

	got, err := f.repo.FetchBatchPendingQueueMetrics(ctx, []string{"q1"})
	if err != nil {
		t.Fatalf("FetchBatchPendingQueueMetrics() unexpected error: %v", err)
	}
	compareQueueMetricRows(t, got, []storage.QueueMetricRow{
		{QueueName: "q1", Count: 1, TotalUnits: 8, ResourceType: resourceTypeCPU},
		{QueueName: "q1", Count: 1, TotalUnits: 2, ResourceType: "GPU"},
	})
}

func TestSaveWorkUnitStatusHistory_StoresOneRowPerResource(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)

	eid := int64(9)
	scheduling := essmsgpb.WorkUnitStatus_SCHEDULING
	running := essmsgpb.WorkUnitStatus_RUNNING
	timestamp := time.Date(2026, time.April, 13, 0, 0, 0, 0, time.UTC)
	resources := map[string]int64{resourceTypeCPU: 8, "GPU": 2}
	if err := f.save(ctx, 1, 101, &eid, "q1", &scheduling, &running, resources, timestamp); err != nil {
		t.Fatalf("SaveWorkUnitStatusHistory() unexpected error: %v", err)
	}

	stored := queryStoredHistory(ctx, t, f.pool, 1, 101)
	if len(stored) != 2 {
		t.Fatalf("SaveWorkUnitStatusHistory() wrote %d rows, want 2", len(stored))
	}
	if stored[0].HistoryID == stored[1].HistoryID {
		t.Errorf("SaveWorkUnitStatusHistory() wrote rows with the same HistoryId %d", stored[0].HistoryID)
	}
	wantTypes := []string{resourceTypeCPU, "GPU"}
	wantUnits := []int64{8, 2}
	for i, row := range stored {
		if row.ExecutionID == nil || *row.ExecutionID != eid {
			t.Errorf("row %d ExecutionId = %v, want %d", i, row.ExecutionID, eid)
		}
		if row.QueueName == nil || *row.QueueName != "q1" {
			t.Errorf("row %d QueueName = %v, want q1", i, row.QueueName)
		}
		if row.PreviousState == nil || *row.PreviousState != int64(scheduling) {
			t.Errorf("row %d PreviousState = %v, want %d", i, row.PreviousState, scheduling)
		}
		if row.NewState != int64(running) {
			t.Errorf("row %d NewState = %d, want %d", i, row.NewState, running)
		}
		if row.ResourceType == nil || *row.ResourceType != wantTypes[i] {
			t.Errorf("row %d ResourceType = %v, want %q", i, row.ResourceType, wantTypes[i])
		}
		if row.ResourceUnits == nil || *row.ResourceUnits != wantUnits[i] {
			t.Errorf("row %d ResourceUnits = %v, want %d", i, row.ResourceUnits, wantUnits[i])
		}
		if !row.CreateTime.Equal(timestamp) {
			t.Errorf("row %d CreateTime = %v, want %v", i, row.CreateTime, timestamp)
		}
		if row.HistoryID < 1 {
			t.Errorf("row %d HistoryId = %d, want >= 1", i, row.HistoryID)
		}
	}
}

// A first transition has no previous state or execution and stores NULL for both; a zero
// timestamp defaults to the time of the call.
func TestSaveWorkUnitStatusHistory_StoresNullColumns(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)

	initializing := essmsgpb.WorkUnitStatus_INITIALIZING
	before := time.Now().Truncate(time.Microsecond)
	if err := f.save(ctx, 1, 101, nil, "q1", nil, &initializing, map[string]int64{"": 0}, time.Time{}); err != nil {
		t.Fatalf("SaveWorkUnitStatusHistory() unexpected error: %v", err)
	}

	stored := queryStoredHistory(ctx, t, f.pool, 1, 101)
	if len(stored) != 1 {
		t.Fatalf("SaveWorkUnitStatusHistory() wrote %d rows, want 1", len(stored))
	}
	row := stored[0]
	if row.ExecutionID != nil || row.PreviousState != nil {
		t.Errorf("ExecutionId, PreviousState = %v, %v, want NULL, NULL", row.ExecutionID, row.PreviousState)
	}
	if row.ResourceType == nil || *row.ResourceType != "" || row.ResourceUnits == nil || *row.ResourceUnits != 0 {
		t.Errorf("ResourceType, ResourceUnits = %v, %v, want \"\", 0", row.ResourceType, row.ResourceUnits)
	}
	if row.CreateTime.Before(before) || row.CreateTime.After(time.Now()) {
		t.Errorf("CreateTime = %v, want between %v and now", row.CreateTime, before)
	}
}

func TestSaveWorkUnitStatusHistory_Resources(t *testing.T) {
	running := essmsgpb.WorkUnitStatus_RUNNING
	tests := []struct {
		name      string
		newState  *essmsgpb.WorkUnitStatus_WorkUnitState
		resources map[string]int64
		wantErr   bool
		wantRows  int64
	}{
		{name: "nil resources write nothing", newState: &running, resources: nil, wantRows: 0},
		{name: "empty resources write nothing", newState: &running, resources: map[string]int64{}, wantRows: 0},
		{name: "nil new state without resources writes nothing", newState: nil, resources: nil, wantRows: 0},
		{name: "nil new state with resources fails", newState: nil, resources: map[string]int64{resourceTypeCPU: 1}, wantErr: true, wantRows: 0},
		{name: "three resources write three rows", newState: &running, resources: map[string]int64{"a": 1, "b": 2, "c": 3}, wantRows: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t)

			err := f.save(ctx, 1, 101, nil, "q1", nil, tt.newState, tt.resources, time.Now())
			if (err != nil) != tt.wantErr {
				t.Fatalf("SaveWorkUnitStatusHistory() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := countHistoryRows(ctx, t, f.pool); got != tt.wantRows {
				t.Errorf("SaveWorkUnitStatusHistory() wrote %d rows, want %d", got, tt.wantRows)
			}
		})
	}
}

// A failed transaction callback rolls the inserts back.
func TestSaveWorkUnitStatusHistory_RolledBack(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)

	running := essmsgpb.WorkUnitStatus_RUNNING
	errAbort := errors.New("abort")
	err := pgdb.RunInTx(ctx, f.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := f.repo.WithTx(tx).SaveWorkUnitStatusHistory(ctx, 1, 101, nil, "q1", nil, &running, map[string]int64{resourceTypeCPU: 1}, time.Now()); err != nil {
			return err
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("RunInTx() error = %v, want %v", err, errAbort)
	}
	if got := countHistoryRows(ctx, t, f.pool); got != 0 {
		t.Errorf("rolled back transaction left %d rows, want 0", got)
	}
}

// Concurrent transactions record transitions independently: inserts take no row locks and the
// random HistoryId keeps the primary keys distinct, so none is lost.
func TestSaveWorkUnitStatusHistory_ConcurrentInserts(t *testing.T) {
	const workers = 10
	ctx := t.Context()
	f := newFixture(t)

	running := essmsgpb.WorkUnitStatus_RUNNING
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			// Half of the workers share one work unit, so their rows collide on everything but
			// HistoryId.
			errs[i] = f.save(ctx, 1, int64(100+i%2), nil, "q1", nil, &running, map[string]int64{resourceTypeCPU: 1}, time.Now().Add(-time.Minute))
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("SaveWorkUnitStatusHistory() unexpected error: %v", err)
	}

	if got := countHistoryRows(ctx, t, f.pool); got != workers {
		t.Errorf("concurrent SaveWorkUnitStatusHistory() wrote %d rows, want %d", got, workers)
	}
	got, err := f.repo.FetchBatchQueueVelocity(ctx, []string{"q1"}, time.Hour)
	if err != nil {
		t.Fatalf("FetchBatchQueueVelocity() unexpected error: %v", err)
	}
	compareQueueMetricRows(t, got, []storage.QueueMetricRow{
		{QueueName: "q1", Count: 2, TotalUnits: workers, ResourceType: resourceTypeCPU},
	})
}
