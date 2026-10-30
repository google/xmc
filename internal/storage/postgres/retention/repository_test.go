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

package retention

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const (
	testXID = 1
	testWID = 1
	day     = 24 * time.Hour

	severityInfo  int64 = 20
	severityError int64 = 40
	severityFatal int64 = 50
)

// remainingRows lists the keys of the rows left in each table with a retention policy.
type remainingRows struct {
	StatusMessages []int64
	StatusHistory  []int64
	PausedTasks    []string
	CLILoginCodes  []string
}

func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	postgrestest.SeedWorkUnit(t.Context(), t, essPool, testXID, testWID)
	return NewRepository(essPool), essPool
}

func ago(d time.Duration) time.Time {
	return time.Now().Add(-d)
}

func mustExec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("Exec(%q) unexpected error: %v", sql, err)
	}
}

func insertStatusMessage(ctx context.Context, t *testing.T, pool *pgxpool.Pool, messageID, severity int64, createTime time.Time) {
	t.Helper()
	mustExec(ctx, t, pool, `INSERT INTO StatusMessages (Xid, MessageId, Severity, Source, UserFacingText, CreateTime)
VALUES ($1, $2, $3, 0, '', $4)`, testXID, messageID, severity, createTime)
}

func insertStatusHistory(ctx context.Context, t *testing.T, pool *pgxpool.Pool, historyID int64, createTime time.Time) {
	t.Helper()
	mustExec(ctx, t, pool, `INSERT INTO WorkUnitStatusHistory (Xid, WorkUnitId, HistoryId, NewState, CreateTime)
VALUES ($1, $2, $3, 0, $4)`, testXID, testWID, historyID, createTime)
}

// insertPausedTask inserts a paused task created a year ago; a nil processedTime keeps it pending.
func insertPausedTask(ctx context.Context, t *testing.T, pool *pgxpool.Pool, taskID string, processedTime *time.Time) {
	t.Helper()
	mustExec(ctx, t, pool, `INSERT INTO PausedTasks (Xid, WorkUnitId, TaskId, Target, ActionProto, OperationName, TraceId, Priority, CreateTime, ProcessedTime)
VALUES ($1, $2, $3, '', '\x', '', '', 0, $4, $5)`, testXID, testWID, taskID, ago(365*day), processedTime)
}

// insertCLILoginCode inserts a CLI login code whose hash and email are both email.
func insertCLILoginCode(ctx context.Context, t *testing.T, pool *pgxpool.Pool, email string, expiresAt time.Time) {
	t.Helper()
	mustExec(ctx, t, pool, `INSERT INTO CliLoginCodes (CodeHash, Email, CodeChallenge, ExpiresAt)
VALUES ($1, $2, '', $3)`, []byte(email), email, expiresAt)
}

func queryColumn[T any](ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string) []T {
	t.Helper()
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		t.Fatalf("Query(%q) unexpected error: %v", sql, err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[T])
	if err != nil {
		t.Fatalf("CollectRows(%q) unexpected error: %v", sql, err)
	}
	return values
}

func readRemaining(ctx context.Context, t *testing.T, pool *pgxpool.Pool) remainingRows {
	t.Helper()
	return remainingRows{
		StatusMessages: queryColumn[int64](ctx, t, pool, `SELECT MessageId FROM StatusMessages ORDER BY MessageId`),
		StatusHistory:  queryColumn[int64](ctx, t, pool, `SELECT HistoryId FROM WorkUnitStatusHistory ORDER BY HistoryId`),
		PausedTasks:    queryColumn[string](ctx, t, pool, `SELECT TaskId FROM PausedTasks ORDER BY TaskId`),
		CLILoginCodes:  queryColumn[string](ctx, t, pool, `SELECT Email FROM CliLoginCodes ORDER BY Email`),
	}
}

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	if repo := NewRepository(pool); repo.pool != pool {
		t.Errorf("NewRepository(pool).pool = %p, want %p", repo.pool, pool)
	}
}

func TestRepository_DeleteExpiredRows_InvalidCalls(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name      string
		ctx       context.Context
		pool      *pgxpool.Pool
		batchSize int
		wantErr   error
	}{
		{name: "zero batch size", ctx: context.Background(), pool: &pgxpool.Pool{}, batchSize: 0, wantErr: grpcerr.ErrInvalidArgument},
		{name: "negative batch size", ctx: context.Background(), pool: &pgxpool.Pool{}, batchSize: -1, wantErr: grpcerr.ErrInvalidArgument},
		{name: "nil pool", ctx: context.Background(), pool: nil, batchSize: 10, wantErr: grpcerr.ErrFailedPrecondition},
		{name: "canceled context", ctx: canceledCtx, pool: &pgxpool.Pool{}, batchSize: 10, wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewRepository(tt.pool).DeleteExpiredRows(tt.ctx, tt.batchSize)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("DeleteExpiredRows() error = %v, want %v", err, tt.wantErr)
			}
			if got != 0 {
				t.Errorf("DeleteExpiredRows() = %d, want 0", got)
			}
		})
	}
}

func TestRepository_DeleteExpiredRows_DeletesOnlyExpiredRows(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	// StatusMessages expire after 30 days, or 180 days for ERROR and FATAL: 1, 4 and 6 expire.
	insertStatusMessage(ctx, t, pool, 1, severityInfo, ago(31*day))
	insertStatusMessage(ctx, t, pool, 2, severityInfo, ago(29*day))
	insertStatusMessage(ctx, t, pool, 3, severityError, ago(31*day))
	insertStatusMessage(ctx, t, pool, 4, severityError, ago(181*day))
	insertStatusMessage(ctx, t, pool, 5, severityFatal, ago(179*day))
	insertStatusMessage(ctx, t, pool, 6, severityFatal, ago(181*day))
	// WorkUnitStatusHistory rows expire after 30 days: 1 expires.
	insertStatusHistory(ctx, t, pool, 1, ago(31*day))
	insertStatusHistory(ctx, t, pool, 2, ago(29*day))
	// PausedTasks expire 7 days after processing and never while pending: processed-8d expires.
	processed6DaysAgo, processed8DaysAgo := ago(6*day), ago(8*day)
	insertPausedTask(ctx, t, pool, "pending", nil)
	insertPausedTask(ctx, t, pool, "processed-6d", &processed6DaysAgo)
	insertPausedTask(ctx, t, pool, "processed-8d", &processed8DaysAgo)
	// CliLoginCodes expire one day after ExpiresAt: expired-2d expires.
	insertCLILoginCode(ctx, t, pool, "expired-12h", ago(12*time.Hour))
	insertCLILoginCode(ctx, t, pool, "expired-2d", ago(2*day))
	insertCLILoginCode(ctx, t, pool, "valid", ago(-time.Hour))

	got, err := repo.DeleteExpiredRows(ctx, 100)
	if err != nil {
		t.Fatalf("DeleteExpiredRows() unexpected error: %v", err)
	}
	if want := int64(6); got != want {
		t.Errorf("DeleteExpiredRows() = %d, want %d", got, want)
	}
	want := remainingRows{
		StatusMessages: []int64{2, 3, 5},
		StatusHistory:  []int64{2},
		PausedTasks:    []string{"pending", "processed-6d"},
		CLILoginCodes:  []string{"expired-12h", "valid"},
	}
	if diff := cmp.Diff(want, readRemaining(ctx, t, pool)); diff != "" {
		t.Errorf("remaining rows mismatch (-want +got):\n%s", diff)
	}

	again, err := repo.DeleteExpiredRows(ctx, 100)
	if err != nil {
		t.Fatalf("second DeleteExpiredRows() unexpected error: %v", err)
	}
	if again != 0 {
		t.Errorf("second DeleteExpiredRows() = %d, want 0", again)
	}
}

func TestRepository_DeleteExpiredRows_LimitsEachTableToBatchSize(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	for i := range 3 {
		insertStatusHistory(ctx, t, pool, int64(i+1), ago(31*day))
		insertCLILoginCode(ctx, t, pool, fmt.Sprintf("expired-%d", i), ago(2*day))
	}

	// Each call deletes up to 2 rows from each table: 2+2, then the remaining 1+1, then nothing.
	var got []int64
	for range 3 {
		n, err := repo.DeleteExpiredRows(ctx, 2)
		if err != nil {
			t.Fatalf("DeleteExpiredRows() unexpected error: %v", err)
		}
		got = append(got, n)
	}
	if diff := cmp.Diff([]int64{4, 2, 0}, got); diff != "" {
		t.Errorf("DeleteExpiredRows() results mismatch (-want +got):\n%s", diff)
	}
}

func TestRepository_DeleteExpiredRows_SkipsWhileSweepLockIsHeld(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertStatusHistory(ctx, t, pool, 1, ago(31*day))

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() unexpected error: %v", err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sweepLockKey); err != nil {
		t.Fatalf("pg_advisory_xact_lock() unexpected error: %v", err)
	}
	got, err := repo.DeleteExpiredRows(ctx, 10)
	if err != nil {
		t.Fatalf("DeleteExpiredRows() while locked unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("DeleteExpiredRows() while locked = %d, want 0", got)
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() unexpected error: %v", err)
	}

	got, err = repo.DeleteExpiredRows(ctx, 10)
	if err != nil {
		t.Fatalf("DeleteExpiredRows() after unlock unexpected error: %v", err)
	}
	if got != 1 {
		t.Errorf("DeleteExpiredRows() after unlock = %d, want 1", got)
	}
}

func TestRepository_DeleteExpiredRows_ConcurrentSweepsDeleteEachRowOnce(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	const rows, batchSize, sweepers = 20, 3, 5
	for i := range rows {
		insertStatusHistory(ctx, t, pool, int64(i+1), ago(31*day))
	}

	// Each sweeper repeats until a call deletes fewer than batchSize rows, either because the table
	// is drained or because another sweeper holds the lock and keeps sweeping.
	var total atomic.Int64
	var wg sync.WaitGroup
	for range sweepers {
		wg.Go(func() {
			for {
				n, err := repo.DeleteExpiredRows(ctx, batchSize)
				if err != nil {
					t.Errorf("DeleteExpiredRows() unexpected error: %v", err)
					return
				}
				total.Add(n)
				if n < batchSize {
					return
				}
			}
		})
	}
	wg.Wait()

	if got := total.Load(); got != rows {
		t.Errorf("total rows deleted = %d, want %d", got, rows)
	}
	if diff := cmp.Diff(remainingRows{}, readRemaining(ctx, t, pool), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("remaining rows mismatch (-want +got):\n%s", diff)
	}
}
