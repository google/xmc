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

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
)

func newLazyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:5432/testdb")
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig() failed: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig() failed: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNewStorage_NilPool(t *testing.T) {
	s, err := NewStorage(nil)
	if err == nil {
		t.Fatalf("NewStorage(nil) = %v, want error", s)
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("status.Code(err) = %v, want %v", got, codes.InvalidArgument)
	}
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("errors.Is(err, ErrInvalidArgument) = false, err = %v", err)
	}
}

func TestStorage(t *testing.T) {
	ctx := context.Background()
	s, err := NewStorage(newLazyTestPool(t))
	if err != nil {
		t.Fatalf("NewStorage() failed: %v", err)
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "PauseState",
			check: func(t *testing.T) {
				if s.pauseStateRepository == nil || s.PauseState() != s.pauseStateRepository {
					t.Error("PauseState() did not return initialized pauseStateRepository")
				}
			},
		},
		{
			name: "PausedTasks",
			check: func(t *testing.T) {
				if s.pausedTaskRepository == nil || s.PausedTasks() != s.pausedTaskRepository {
					t.Error("PausedTasks() did not return initialized pausedTaskRepository")
				}
			},
		},
		{
			name: "newTransaction",
			check: func(t *testing.T) {
				// Populating all 7 Transaction fields is verified here; binding the DBTX
				// handle into each wrapper's unexported tx field is covered by the
				// per-subpackage TestNewRepository_BindsPoolAndTx tests.
				tx := s.newTransaction(&fakeTx{})
				if tx.WorkUnits == nil || tx.Experiments == nil || tx.XIDs == nil ||
					tx.StatusMessages == nil || tx.StatusHistory == nil ||
					tx.PauseState == nil || tx.PausedTasks == nil {
					t.Errorf("newTransaction() returned incomplete transaction: %+v", tx)
				}
			},
		},
		{
			name: "RunInTransaction nil pool",
			check: func(t *testing.T) {
				err := (&Storage{}).RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
					return nil
				})
				if got := status.Code(err); got != codes.FailedPrecondition {
					t.Errorf("RunInTransaction() code = %v, want %v (err: %v)", got, codes.FailedPrecondition, err)
				}
			},
		},
		{
			name: "RunInTransaction nil callback",
			check: func(t *testing.T) {
				err := s.RunInTransaction(ctx, nil)
				if got := status.Code(err); got != codes.InvalidArgument {
					t.Errorf("RunInTransaction(nil) code = %v, want %v (err: %v)", got, codes.InvalidArgument, err)
				}
			},
		},
		{
			name: "RunInTransaction canceled context",
			check: func(t *testing.T) {
				canceledCtx, cancel := context.WithCancel(ctx)
				cancel()
				err := s.RunInTransaction(canceledCtx, func(ctx context.Context, tx *storage.Transaction) error {
					return nil
				})
				if !errors.Is(err, context.Canceled) {
					t.Errorf("RunInTransaction(canceledCtx) = %v, want context.Canceled", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t)
		})
	}
}

func TestStorage_RunInTransactionUsesBeginner(t *testing.T) {
	callbackErr := errors.New("callback failed")
	tests := []struct {
		name           string
		fnErr          error
		wantCommitted  int
		wantRolledBack int
	}{
		{name: "commits after the callback succeeds", wantCommitted: 1},
		{name: "rolls back when the callback fails", fnErr: callbackErr, wantRolledBack: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewStorage(newLazyTestPool(t))
			if err != nil {
				t.Fatalf("NewStorage() failed: %v", err)
			}
			pgTx := &fakeTx{}
			s.beginner = &fakeBeginner{txs: []*fakeTx{pgTx}}

			var gotTx *storage.Transaction
			err = s.RunInTransaction(context.Background(), func(_ context.Context, tx *storage.Transaction) error {
				gotTx = tx
				return tt.fnErr
			})
			if !errors.Is(err, tt.fnErr) {
				t.Fatalf("RunInTransaction() error = %v, want %v", err, tt.fnErr)
			}
			if gotTx == nil {
				t.Error("RunInTransaction() did not pass a transaction to the callback")
			}
			if pgTx.committed != tt.wantCommitted || pgTx.rolledBack != tt.wantRolledBack {
				t.Errorf("committed=%d rolledBack=%d, want %d/%d", pgTx.committed, pgTx.rolledBack, tt.wantCommitted, tt.wantRolledBack)
			}
		})
	}
}
