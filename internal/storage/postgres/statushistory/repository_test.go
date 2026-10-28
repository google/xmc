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
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

var ignoreHistoryIDAndCreateTime = cmpopts.IgnoreFields(postgresdata.WorkUnitStatusHistoryRow{}, "HistoryID", "CreateTime")

func queryStoredHistoryRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []postgresdata.WorkUnitStatusHistoryRow {
	t.Helper()
	const q = `SELECT Xid, WorkUnitId, HistoryId, ExecutionId, QueueName, PreviousState, NewState, ResourceType, ResourceUnits, CreateTime
FROM WorkUnitStatusHistory
ORDER BY Xid, WorkUnitId, ResourceType`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		t.Fatalf("pool.Query() failed: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[postgresdata.WorkUnitStatusHistoryRow])
	if err != nil {
		t.Fatalf("pgx.CollectRows() failed: %v", err)
	}
	return got
}

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestQueueMetricRow(t *testing.T) {
	row := storage.QueueMetricRow{
		QueueName:    "cq-gpu",
		Count:        12,
		TotalUnits:   96,
		ResourceType: "GPU H100",
	}
	window := 1 * time.Hour
	if rate := float64(row.Count) / window.Hours(); rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}

func TestFetchBatchQueueMetrics_NilClient(t *testing.T) {
	ctx := t.Context()
	repo := NewRepository(nil)

	batchVel, err := repo.FetchBatchQueueVelocity(ctx, []string{"cq-gpu"}, time.Hour)
	if err != nil || len(batchVel) != 0 {
		t.Errorf("FetchBatchQueueVelocity(nil pool) = (%+v, %v), want (empty, nil)", batchVel, err)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(ctx, []string{"cq-gpu"})
	if err != nil || len(batchPending) != 0 {
		t.Errorf("FetchBatchPendingQueueMetrics(nil pool) = (%+v, %v), want (empty, nil)", batchPending, err)
	}
}

func TestTxRepository_SaveWorkUnitStatusHistory_Validation(t *testing.T) {
	ctx := t.Context()
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	running := essmsgpb.WorkUnitStatus_RUNNING
	eid := int64(1)
	dummyTx := &pgxpool.Pool{}

	tests := []struct {
		name     string
		tx       pgdb.DBTX
		ctx      context.Context
		newState *essmsgpb.WorkUnitStatus_WorkUnitState
	}{
		{name: "nil context", tx: dummyTx, ctx: nil, newState: &running},
		{name: "nil transaction", tx: nil, ctx: ctx, newState: &running},
		{name: "canceled context", tx: dummyTx, ctx: canceledCtx, newState: &running},
		{name: "nil newState", tx: dummyTx, ctx: ctx, newState: nil},
	}

	repo := NewRepository(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.WithTx(tt.tx).SaveWorkUnitStatusHistory(tt.ctx, 1, 1, &eid, "q1", nil, tt.newState, map[string]int64{"": 0}, time.Now())
			if err == nil {
				t.Fatalf("SaveWorkUnitStatusHistory(%s) succeeded, want error", tt.name)
			}
		})
	}
}

func TestTxRepository_SaveWorkUnitStatusHistory_Persistence(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	eid := int64(7)
	prevScheduling := essmsgpb.WorkUnitStatus_SCHEDULING
	newRunning := essmsgpb.WorkUnitStatus_RUNNING
	fixedTime := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	err := pgdb.RunInTx(ctx, essPool, func(ctx context.Context, tx pgx.Tx) error {
		txRepo := repo.WithTx(tx)
		if err := txRepo.SaveWorkUnitStatusHistory(ctx, 10, 101, &eid, "q-gpu", &prevScheduling, &newRunning, map[string]int64{"CPU": 16, "GPU": 4}, fixedTime); err != nil {
			return err
		}
		if err := txRepo.SaveWorkUnitStatusHistory(ctx, 10, 102, nil, "q-cpu", nil, &newRunning, map[string]int64{"": 0}, time.Time{}); err != nil {
			return err
		}
		return txRepo.SaveWorkUnitStatusHistory(ctx, 10, 103, nil, "q-empty", nil, &newRunning, nil, fixedTime)
	})
	if err != nil {
		t.Fatalf("SaveWorkUnitStatusHistory() in transaction failed: %v", err)
	}

	got := queryStoredHistoryRows(ctx, t, essPool)
	want := []postgresdata.WorkUnitStatusHistoryRow{
		{
			Xid:           10,
			WorkUnitID:    101,
			ExecutionID:   pgtype.Int8{Int64: 7, Valid: true},
			QueueName:     "q-gpu",
			PreviousState: pgtype.Int8{Int64: int64(essmsgpb.WorkUnitStatus_SCHEDULING), Valid: true},
			NewState:      int64(essmsgpb.WorkUnitStatus_RUNNING),
			ResourceType:  "CPU",
			ResourceUnits: 16,
		},
		{
			Xid:           10,
			WorkUnitID:    101,
			ExecutionID:   pgtype.Int8{Int64: 7, Valid: true},
			QueueName:     "q-gpu",
			PreviousState: pgtype.Int8{Int64: int64(essmsgpb.WorkUnitStatus_SCHEDULING), Valid: true},
			NewState:      int64(essmsgpb.WorkUnitStatus_RUNNING),
			ResourceType:  "GPU",
			ResourceUnits: 4,
		},
		{
			Xid:           10,
			WorkUnitID:    102,
			ExecutionID:   pgtype.Int8{Valid: false},
			QueueName:     "q-cpu",
			PreviousState: pgtype.Int8{Valid: false},
			NewState:      int64(essmsgpb.WorkUnitStatus_RUNNING),
			ResourceType:  "",
			ResourceUnits: 0,
		},
	}
	if diff := gocmp.Diff(want, got, ignoreHistoryIDAndCreateTime); diff != "" {
		t.Fatalf("stored WorkUnitStatusHistory rows mismatch (-want +got):\n%s", diff)
	}
	for i, r := range got {
		if r.HistoryID <= 0 {
			t.Errorf("row[%d].HistoryID = %d, want > 0", i, r.HistoryID)
		}
		if r.CreateTime.IsZero() {
			t.Errorf("row[%d].CreateTime is zero", i)
		}
	}
	if !got[0].CreateTime.Equal(fixedTime) {
		t.Errorf("row[0].CreateTime = %v, want %v", got[0].CreateTime, fixedTime)
	}
}

func TestFetchBatchQueueMetrics_CanceledContext(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := repo.FetchBatchQueueVelocity(canceledCtx, []string{"q1"}, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchBatchQueueVelocity(canceled ctx) = %v, want %v", err, context.Canceled)
	}
	if _, err := repo.FetchBatchPendingQueueMetrics(canceledCtx, []string{"q1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchBatchPendingQueueMetrics(canceled ctx) = %v, want %v", err, context.Canceled)
	}
}
