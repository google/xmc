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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
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
	rate := float64(row.Count) / window.Hours()

	if rate != 12.0 {
		t.Errorf("rate = %f, want 12.0", rate)
	}
}

// A repository without a pool reports no metrics rather than an error, as the Spanner repository
// does for a nil client.
func TestFetchBatchQueueMetrics_NilPool(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)
	batchVel, err := repo.FetchBatchQueueVelocity(ctx, []string{"cq-gpu"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("FetchBatchQueueVelocity() with nil pool unexpected error: %v", err)
	}
	if len(batchVel) != 0 {
		t.Errorf("FetchBatchQueueVelocity() with nil pool = %+v, want empty", batchVel)
	}

	batchPending, err := repo.FetchBatchPendingQueueMetrics(ctx, []string{"cq-gpu"})
	if err != nil {
		t.Fatalf("FetchBatchPendingQueueMetrics() with nil pool unexpected error: %v", err)
	}
	if len(batchPending) != 0 {
		t.Errorf("FetchBatchPendingQueueMetrics() with nil pool = %+v, want empty", batchPending)
	}
}

// The validation checks run before the transaction is touched, so a nil transaction handle is
// enough to exercise them.
func TestSaveWorkUnitStatusHistory_Validation(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	running := essmsgpb.WorkUnitStatus_RUNNING
	eid := int64(1)

	tests := []struct {
		name string
		ctx  context.Context
		tx   pgdb.DBTX
	}{
		{name: "nil_context", ctx: nil, tx: &pgxpool.Pool{}},
		{name: "canceled_context", ctx: canceledCtx, tx: &pgxpool.Pool{}},
		{name: "nil_transaction", ctx: context.Background(), tx: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewRepository(nil).WithTx(tt.tx).SaveWorkUnitStatusHistory(tt.ctx, 1, 1, &eid, "q1", nil, &running, map[string]int64{"": 0}, time.Now())
			if err == nil {
				t.Fatalf("SaveWorkUnitStatusHistory() succeeded, want error")
			}
		})
	}
}

func TestHistoryInsertArgs(t *testing.T) {
	createTime := time.Date(2026, time.April, 13, 12, 0, 0, 0, time.UTC)
	eid := int64(5)
	scheduling := essmsgpb.WorkUnitStatus_SCHEDULING
	running := essmsgpb.WorkUnitStatus_RUNNING
	schedulingValue := int64(scheduling)

	tests := []struct {
		name      string
		eid       *int64
		prevState *essmsgpb.WorkUnitStatus_WorkUnitState
		newState  *essmsgpb.WorkUnitStatus_WorkUnitState
		want      []any
		wantErr   bool
	}{
		{
			name:      "all fields",
			eid:       &eid,
			prevState: &scheduling,
			newState:  &running,
			want:      []any{int64(1), int64(2), int64(3), &eid, "q1", &schedulingValue, int64(running), "CPU", int64(10), createTime},
		},
		{
			name:     "nullable fields are NULL",
			newState: &running,
			want:     []any{int64(1), int64(2), int64(3), (*int64)(nil), "q1", (*int64)(nil), int64(running), "CPU", int64(10), createTime},
		},
		{
			name:      "nil new state",
			prevState: &scheduling,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := historyInsertArgs(1, 2, 3, tt.eid, "q1", tt.prevState, tt.newState, "CPU", 10, createTime)
			if (err != nil) != tt.wantErr {
				t.Fatalf("historyInsertArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("historyInsertArgs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A zero timestamp defaults to the time of the call.
func TestHistoryInsertArgs_DefaultsCreateTime(t *testing.T) {
	running := essmsgpb.WorkUnitStatus_RUNNING
	before := time.Now()
	got, err := historyInsertArgs(1, 2, 3, nil, "q1", nil, &running, "CPU", 10, time.Time{})
	if err != nil {
		t.Fatalf("historyInsertArgs() unexpected error: %v", err)
	}
	createTime, ok := got[9].(time.Time)
	if !ok {
		t.Fatalf("CreateTime argument = %T, want time.Time", got[9])
	}
	if createTime.Before(before) || createTime.After(time.Now()) {
		t.Errorf("CreateTime argument = %v, want between %v and now", createTime, before)
	}
}

func TestGenerateHistoryID_IsPositive(t *testing.T) {
	for range 1000 {
		if id := generateHistoryID(); id < 1 {
			t.Fatalf("generateHistoryID() = %d, want >= 1", id)
		}
	}
}
