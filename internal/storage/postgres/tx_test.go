// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

type fakeTx struct {
	pgx.Tx
	commitErr  error
	committed  int
	rolledBack int
}

func (f *fakeTx) Commit(_ context.Context) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.committed++
	return nil
}

func (f *fakeTx) Rollback(_ context.Context) error {
	f.rolledBack++
	return nil
}

type fakeBeginner struct {
	txs      []*fakeTx
	beginErr error
	begins   int
}

func (b *fakeBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	b.begins++
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	idx := b.begins - 1
	if idx < len(b.txs) {
		return b.txs[idx], nil
	}
	tx := &fakeTx{}
	b.txs = append(b.txs, tx)
	return tx, nil
}

func fastRetryConfig() txRetryConfig {
	return txRetryConfig{
		maxAttempts:    DefaultMaxTxAttempts,
		initialBackoff: time.Millisecond,
		maxBackoff:     4 * time.Millisecond,
	}
}

func TestRunInTxNilValidation(t *testing.T) {
	t.Run("nil pool returns FailedPrecondition", func(t *testing.T) {
		err := RunInTx(context.Background(), nil, func(context.Context, pgx.Tx) error {
			return nil
		})
		if !errors.Is(err, grpcerr.ErrFailedPrecondition) {
			t.Errorf("errors.Is(%v, grpcerr.ErrFailedPrecondition) = false, want true", err)
		}
		if got := status.Code(err); got != codes.FailedPrecondition {
			t.Errorf("status.Code() = %v, want %v", got, codes.FailedPrecondition)
		}
	})

	t.Run("nil callback returns InvalidArgument", func(t *testing.T) {
		beginner := &fakeBeginner{}
		err := runInTx(context.Background(), beginner, fastRetryConfig(), nil)
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("errors.Is(%v, grpcerr.ErrInvalidArgument) = false, want true", err)
		}
		if beginner.begins != 0 {
			t.Errorf("begins = %d, want 0", beginner.begins)
		}
	})
}

func TestRunInTxExecutionAndRetries(t *testing.T) {
	serializationErr := &pgconn.PgError{Code: pgdb.SQLStateSerializationFailure, Message: "could not serialize"}
	deadlockErr := &pgconn.PgError{Code: pgdb.SQLStateDeadlockDetected, Message: "deadlock detected"}
	nonRetryableErr := errors.New("permanent validation failure")

	t.Run("commits on first attempt success", func(t *testing.T) {
		tx := &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx}}
		calls := 0

		err := runInTx(context.Background(), beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("runInTx() unexpected error: %v", err)
		}
		if calls != 1 || tx.committed != 1 || tx.rolledBack != 0 {
			t.Errorf("calls=%d committed=%d rolledBack=%d, want 1/1/0", calls, tx.committed, tx.rolledBack)
		}
	})

	t.Run("rolls back without retry on non-retryable error", func(t *testing.T) {
		tx := &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx}}

		err := runInTx(context.Background(), beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			return nonRetryableErr
		})
		if !errors.Is(err, nonRetryableErr) {
			t.Fatalf("runInTx() = %v, want %v", err, nonRetryableErr)
		}
		if beginner.begins != 1 || tx.committed != 0 || tx.rolledBack != 1 {
			t.Errorf("begins=%d committed=%d rolledBack=%d, want 1/0/1", beginner.begins, tx.committed, tx.rolledBack)
		}
	})

	t.Run("retries serialization_failure 40001 and deadlock_detected 40P01 until success", func(t *testing.T) {
		tx1, tx2, tx3 := &fakeTx{}, &fakeTx{}, &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx1, tx2, tx3}}
		attempt := 0

		err := runInTx(context.Background(), beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			attempt++
			switch attempt {
			case 1:
				return serializationErr
			case 2:
				return deadlockErr
			default:
				return nil
			}
		})
		if err != nil {
			t.Fatalf("runInTx() unexpected error: %v", err)
		}
		if attempt != 3 || tx1.rolledBack != 1 || tx2.rolledBack != 1 || tx3.committed != 1 {
			t.Errorf("attempt=%d tx1.rb=%d tx2.rb=%d tx3.commit=%d, want 3/1/1/1", attempt, tx1.rolledBack, tx2.rolledBack, tx3.committed)
		}
	})

	t.Run("retries when Commit returns serialization_failure 40001", func(t *testing.T) {
		tx1 := &fakeTx{commitErr: serializationErr}
		tx2 := &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx1, tx2}}

		err := runInTx(context.Background(), beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			return nil
		})
		if err != nil {
			t.Fatalf("runInTx() unexpected error: %v", err)
		}
		if beginner.begins != 2 || tx1.rolledBack != 1 || tx2.committed != 1 {
			t.Errorf("begins=%d tx1.rb=%d tx2.commit=%d, want 2/1/1", beginner.begins, tx1.rolledBack, tx2.committed)
		}
	})

	t.Run("stops after maxAttempts and returns Aborted error", func(t *testing.T) {
		beginner := &fakeBeginner{}
		cfg := fastRetryConfig()

		err := runInTx(context.Background(), beginner, cfg, func(context.Context, pgx.Tx) error {
			return serializationErr
		})
		if !errors.Is(err, grpcerr.ErrAborted) {
			t.Errorf("errors.Is(%v, grpcerr.ErrAborted) = false, want true", err)
		}
		if !pgdb.IsRetryableTxError(err) {
			t.Errorf("IsRetryableTxError(%v) = false, want true", err)
		}
		if beginner.begins != cfg.maxAttempts {
			t.Errorf("begins = %d, want %d", beginner.begins, cfg.maxAttempts)
		}
	})
}

func TestRunInTxContextAndPanicHandling(t *testing.T) {
	serializationErr := &pgconn.PgError{Code: pgdb.SQLStateSerializationFailure, Message: "could not serialize"}

	t.Run("respects pre-canceled context before beginning transaction", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		beginner := &fakeBeginner{}

		err := runInTx(ctx, beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("runInTx() = %v, want context.Canceled", err)
		}
		if beginner.begins != 0 {
			t.Errorf("begins = %d, want 0", beginner.begins)
		}
	})

	t.Run("aborts backoff sleep when context is canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		beginner := &fakeBeginner{}
		cfg := txRetryConfig{
			maxAttempts:    5,
			initialBackoff: time.Hour,
			maxBackoff:     time.Hour,
		}

		err := runInTx(ctx, beginner, cfg, func(context.Context, pgx.Tx) error {
			cancel()
			return serializationErr
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("runInTx() = %v, want context.Canceled", err)
		}
		if beginner.begins != 1 {
			t.Errorf("begins = %d, want 1", beginner.begins)
		}
	})

	t.Run("rolls back transaction and re-panics when callback panics", func(t *testing.T) {
		tx := &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx}}

		defer func() {
			recovered := recover()
			if recovered != "unexpected panic in tx" {
				t.Fatalf("recover() = %v, want %q", recovered, "unexpected panic in tx")
			}
			if tx.rolledBack != 1 || tx.committed != 0 {
				t.Errorf("rolledBack=%d committed=%d, want 1/0", tx.rolledBack, tx.committed)
			}
		}()

		_ = runInTx(context.Background(), beginner, fastRetryConfig(), func(context.Context, pgx.Tx) error {
			panic("unexpected panic in tx")
		})
	})
}
