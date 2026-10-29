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
				// Populating all 8 Transaction fields is verified here; binding the DBTX
				// handle into each wrapper's unexported tx field is covered by the
				// per-subpackage TestNewRepository_BindsPoolAndTx tests.
				tx := s.newTransaction(s.pool)
				if tx.WorkUnits == nil || tx.Experiments == nil || tx.XIDs == nil ||
					tx.StatusMessages == nil || tx.StatusHistory == nil ||
					tx.PauseState == nil || tx.PausedTasks == nil || tx.Components == nil {
					t.Errorf("newTransaction() returned incomplete transaction: %+v", tx)
				}
			},
		},
		{
			name: "CLILoginCodes",
			check: func(t *testing.T) {
				if s.cliLoginCodeRepository == nil || s.CLILoginCodes() != s.cliLoginCodeRepository {
					t.Error("CLILoginCodes() did not return initialized cliLoginCodeRepository")
				}
			},
		},
		{
			name: "Components",
			check: func(t *testing.T) {
				if s.componentRepository == nil || s.Components() != s.componentRepository {
					t.Error("Components() did not return initialized componentRepository")
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
		{
			name: "RunInTransactionWithCommitTime unimplemented",
			check: func(t *testing.T) {
				ts, err := s.RunInTransactionWithCommitTime(ctx, func(ctx context.Context, tx *storage.Transaction) error {
					return nil
				})
				if got := status.Code(err); got != codes.Unimplemented {
					t.Errorf("RunInTransactionWithCommitTime() code = %v, want %v (err: %v)", got, codes.Unimplemented, err)
				}
				if !ts.IsZero() {
					t.Errorf("RunInTransactionWithCommitTime() timestamp = %v, want zero", ts)
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
