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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
)

const (
	// DefaultMaxTxAttempts is the maximum number of transaction attempts before giving up on retryable errors.
	DefaultMaxTxAttempts = 5

	defaultInitialBackoff = 10 * time.Millisecond
	defaultMaxBackoff     = 200 * time.Millisecond
	rollbackTimeout       = 5 * time.Second
)

type txBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

type txRetryConfig struct {
	txOptions      pgx.TxOptions
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

var defaultTxRetryConfig = txRetryConfig{
	maxAttempts:    DefaultMaxTxAttempts,
	initialBackoff: defaultInitialBackoff,
	maxBackoff:     defaultMaxBackoff,
}

// RunInTx executes fn inside a read-write PostgreSQL transaction, committing on success,
// rolling back on error or panic, and retrying with exponential backoff on transient
// serialization (40001) or deadlock (40P01) errors up to DefaultMaxTxAttempts times.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if pool == nil {
		return WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return runInTx(ctx, pool, defaultTxRetryConfig, fn)
}

// RunInSerializableTx executes fn inside a SERIALIZABLE read-write PostgreSQL transaction with the
// same commit, rollback, and retry semantics as RunInTx.
func RunInSerializableTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if pool == nil {
		return WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return runInSerializableTx(ctx, pool, fn)
}

// RunInReadOnlyTx executes fn inside a REPEATABLE READ READ ONLY PostgreSQL transaction,
// releasing the snapshot via a detached-context Rollback when fn returns or panics.
func RunInReadOnlyTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if pool == nil {
		return WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return runInReadOnlyTx(ctx, pool, fn)
}

func runInSerializableTx(ctx context.Context, beginner txBeginner, fn func(ctx context.Context, tx pgx.Tx) error) error {
	cfg := defaultTxRetryConfig
	cfg.txOptions = pgx.TxOptions{IsoLevel: pgx.Serializable}
	return runInTx(ctx, beginner, cfg, fn)
}

func runInTx(ctx context.Context, beginner txBeginner, cfg txRetryConfig, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if fn == nil {
		return WrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	backoff := cfg.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = runTxAttempt(ctx, beginner, cfg.txOptions, fn)
		if lastErr == nil || !IsRetryableTxError(lastErr) {
			return lastErr
		}
		if attempt < cfg.maxAttempts {
			if err := waitBackoff(ctx, backoff); err != nil {
				return err
			}
			backoff = nextBackoff(backoff, cfg.maxBackoff)
		}
	}
	return TranslateError(lastErr, "transaction")
}

func runTxAttempt(ctx context.Context, beginner txBeginner, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to begin postgres transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackDetached(tx)
	}()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit postgres transaction: %w", err)
	}
	committed = true
	return nil
}

func runInReadOnlyTx(ctx context.Context, beginner txBeginner, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if fn == nil {
		return WrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TranslateError(err, "transaction")
	}
	defer rollbackDetached(tx)
	return fn(ctx, tx)
}

func rollbackDetached(tx pgx.Tx) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_ = tx.Rollback(rollbackCtx)
}

func waitBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextBackoff(current, maxBackoff time.Duration) time.Duration {
	next := current * 2
	if maxBackoff > 0 && next > maxBackoff {
		return maxBackoff
	}
	return next
}
