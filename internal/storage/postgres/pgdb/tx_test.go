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

package pgdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
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
	txOpts   []pgx.TxOptions
	beginErr error
	begins   int
}

func (b *fakeBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	b.txOpts = append(b.txOpts, opts)
	return b.Begin(ctx)
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
	serializationErr := &pgconn.PgError{Code: SQLStateSerializationFailure, Message: "could not serialize"}
	deadlockErr := &pgconn.PgError{Code: SQLStateDeadlockDetected, Message: "deadlock detected"}
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
		if !IsRetryableTxError(err) {
			t.Errorf("IsRetryableTxError(%v) = false, want true", err)
		}
		if beginner.begins != cfg.maxAttempts {
			t.Errorf("begins = %d, want %d", beginner.begins, cfg.maxAttempts)
		}
	})
}

func TestRunInSerializableTx(t *testing.T) {
	serializationErr := &pgconn.PgError{Code: SQLStateSerializationFailure, Message: "could not serialize"}

	if err := RunInSerializableTx(context.Background(), nil, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("RunInSerializableTx(nil pool) = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}

	tests := []struct {
		name string
		run  func(ctx context.Context, beginner txBeginner, fn func(context.Context, pgx.Tx) error) error
		want []pgx.TxOptions
	}{
		{
			name: "runInTx uses server default isolation",
			run: func(ctx context.Context, beginner txBeginner, fn func(context.Context, pgx.Tx) error) error {
				return runInTx(ctx, beginner, fastRetryConfig(), fn)
			},
			want: []pgx.TxOptions{{}},
		},
		{
			name: "runInSerializableTx uses serializable isolation",
			run:  runInSerializableTx,
			want: []pgx.TxOptions{{IsoLevel: pgx.Serializable}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beginner := &fakeBeginner{}
			if err := tt.run(context.Background(), beginner, func(context.Context, pgx.Tx) error { return nil }); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, beginner.txOpts); diff != "" {
				t.Errorf("BeginTx options mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("retries past DefaultMaxTxAttempts until success", func(t *testing.T) {
		beginner := &fakeBeginner{}
		wantAttempts := DefaultMaxTxAttempts + 2
		err := runInSerializableTx(context.Background(), beginner, func(context.Context, pgx.Tx) error {
			if beginner.begins < wantAttempts {
				return serializationErr
			}
			return nil
		})
		if err != nil {
			t.Fatalf("runInSerializableTx() unexpected error: %v", err)
		}
		if beginner.begins != wantAttempts {
			t.Errorf("begins = %d, want %d", beginner.begins, wantAttempts)
		}
	})

	t.Run("stops and returns DeadlineExceeded when caller context expires", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		beginner := &fakeBeginner{}

		err := runInSerializableTx(ctx, beginner, func(context.Context, pgx.Tx) error {
			return serializationErr
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("runInSerializableTx() = %v, want context.DeadlineExceeded", err)
		}
		if beginner.begins < 1 {
			t.Errorf("begins = %d, want >= 1", beginner.begins)
		}
	})

	t.Run("stops and returns Aborted when un-deadlined context exhausts fallbackWindow", func(t *testing.T) {
		beginner := &fakeBeginner{}
		cfg := serializableTxRetryConfig
		cfg.fallbackWindow = 12 * time.Millisecond
		cfg.initialBackoff = time.Millisecond
		cfg.maxBackoff = 2 * time.Millisecond

		err := runInTx(context.Background(), beginner, cfg, func(context.Context, pgx.Tx) error {
			return serializationErr
		})
		if !errors.Is(err, grpcerr.ErrAborted) {
			t.Errorf("errors.Is(%v, grpcerr.ErrAborted) = false, want true", err)
		}
		if beginner.begins < 2 {
			t.Errorf("begins = %d, want >= 2", beginner.begins)
		}
	})

	t.Run("caller deadline overrides fallbackWindow", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		beginner := &fakeBeginner{}
		cfg := serializableTxRetryConfig
		cfg.fallbackWindow = time.Nanosecond
		cfg.initialBackoff = time.Millisecond
		cfg.maxBackoff = time.Millisecond

		err := runInTx(ctx, beginner, cfg, func(context.Context, pgx.Tx) error {
			if beginner.begins < 4 {
				return serializationErr
			}
			return nil
		})
		if err != nil {
			t.Fatalf("runInTx() unexpected error: %v", err)
		}
		if beginner.begins != 4 {
			t.Errorf("begins = %d, want 4", beginner.begins)
		}
	})
}

func TestEqualJitter(t *testing.T) {
	for _, nonJittered := range []time.Duration{-5 * time.Millisecond, 0, 1} {
		if got := equalJitter(nonJittered); got != nonJittered {
			t.Errorf("equalJitter(%v) = %v, want %v", nonJittered, got, nonJittered)
		}
	}
	for _, d := range []time.Duration{2, 11, 10 * time.Millisecond, 200 * time.Millisecond} {
		low := d / 2
		for range 100 {
			got := equalJitter(d)
			if got < low || got > d {
				t.Fatalf("equalJitter(%v) = %v, want within [%v, %v]", d, got, low, d)
			}
		}
	}
}

func TestRunInTxContextAndPanicHandling(t *testing.T) {
	serializationErr := &pgconn.PgError{Code: SQLStateSerializationFailure, Message: "could not serialize"}

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

func TestRunInReadOnlyTx(t *testing.T) {
	if err := RunInReadOnlyTx(context.Background(), nil, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("RunInReadOnlyTx(nil pool) = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	cbErr := errors.New("query failed")
	wantOpts := []pgx.TxOptions{{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name     string
		ctx      context.Context
		beginErr error
		fn       func(context.Context, pgx.Tx) error
		wantErr  error
		wantOpts []pgx.TxOptions
		wantRB   int
	}{
		{name: "nil callback", ctx: context.Background(), wantErr: grpcerr.ErrInvalidArgument},
		{name: "canceled context", ctx: canceledCtx, fn: func(context.Context, pgx.Tx) error { return nil }, wantErr: context.Canceled},
		{name: "BeginTx error translated", ctx: context.Background(), beginErr: &pgconn.PgError{Code: SQLStateSerializationFailure}, fn: func(context.Context, pgx.Tx) error { return nil }, wantErr: grpcerr.ErrAborted, wantOpts: wantOpts},
		{name: "success rolls back snapshot", ctx: context.Background(), fn: func(context.Context, pgx.Tx) error { return nil }, wantOpts: wantOpts, wantRB: 1},
		{name: "callback error rolls back snapshot", ctx: context.Background(), fn: func(context.Context, pgx.Tx) error { return cbErr }, wantErr: cbErr, wantOpts: wantOpts, wantRB: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{}
			beginner := &fakeBeginner{txs: []*fakeTx{tx}, beginErr: tt.beginErr}
			if err := runInReadOnlyTx(tt.ctx, beginner, tt.fn); !errors.Is(err, tt.wantErr) {
				t.Fatalf("runInReadOnlyTx() = %v, want %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantOpts, beginner.txOpts); diff != "" {
				t.Errorf("txOpts mismatch (-want +got):\n%s", diff)
			}
			if tx.rolledBack != tt.wantRB || tx.committed != 0 {
				t.Errorf("rolledBack=%d committed=%d, want %d/0", tx.rolledBack, tx.committed, tt.wantRB)
			}
		})
	}

	t.Run("panic rolls back and re-panics", func(t *testing.T) {
		tx := &fakeTx{}
		beginner := &fakeBeginner{txs: []*fakeTx{tx}}
		defer func() {
			if r := recover(); r != "boom" || tx.rolledBack != 1 || tx.committed != 0 {
				t.Errorf("recover=%v rolledBack=%d committed=%d, want boom/1/0", r, tx.rolledBack, tx.committed)
			}
		}()
		_ = runInReadOnlyTx(context.Background(), beginner, func(context.Context, pgx.Tx) error { panic("boom") })
	})
}
