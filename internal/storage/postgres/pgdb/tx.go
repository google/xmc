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
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
)

const (
	// DefaultMaxTxAttempts is the maximum number of transaction attempts before giving up on retryable errors in RunInTx.
	DefaultMaxTxAttempts = 5

	defaultInitialBackoff          = 10 * time.Millisecond
	defaultMaxBackoff              = 200 * time.Millisecond
	defaultSerializableRetryWindow = 30 * time.Second
	rollbackTimeout                = 5 * time.Second
)

type txBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

type txRetryConfig struct {
	txOptions      pgx.TxOptions
	maxAttempts    int
	fallbackWindow time.Duration
	initialBackoff time.Duration
	maxBackoff     time.Duration
	jitter         func(time.Duration) time.Duration
}

var defaultTxRetryConfig = txRetryConfig{
	maxAttempts:    DefaultMaxTxAttempts,
	initialBackoff: defaultInitialBackoff,
	maxBackoff:     defaultMaxBackoff,
}

var serializableTxRetryConfig = txRetryConfig{
	txOptions:      pgx.TxOptions{IsoLevel: pgx.Serializable},
	fallbackWindow: defaultSerializableRetryWindow,
	initialBackoff: defaultInitialBackoff,
	maxBackoff:     defaultMaxBackoff,
	jitter:         equalJitter,
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

// RunInSerializableTx executes fn inside a SERIALIZABLE read-write PostgreSQL transaction,
// committing on success, rolling back on error or panic, and retrying transient serialization
// (40001) or deadlock (40P01) errors with jittered exponential backoff until ctx expires (or up to
// a 30-second fallback window when ctx has no deadline).
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
	return runInTx(ctx, beginner, serializableTxRetryConfig, fn)
}

func runInTx(ctx context.Context, beginner txBeginner, cfg txRetryConfig, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if fn == nil {
		return WrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	deadline := retryDeadline(ctx, cfg.fallbackWindow)
	backoff := cfg.initialBackoff
	var lastErr error
	for attempt := 1; cfg.maxAttempts <= 0 || attempt <= cfg.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = runTxAttempt(ctx, beginner, cfg.txOptions, fn)
		if lastErr == nil || !IsRetryableTxError(lastErr) {
			return lastErr
		}
		delay, ok := nextRetryDelay(attempt, cfg, backoff, deadline)
		if !ok {
			break
		}
		if err := waitBackoff(ctx, delay); err != nil {
			return err
		}
		backoff = nextBackoff(backoff, cfg.maxBackoff)
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
	if err := ctx.Err(); err != nil || d <= 0 {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func retryDeadline(ctx context.Context, fallbackWindow time.Duration) time.Time {
	if _, ok := ctx.Deadline(); ok || fallbackWindow <= 0 {
		return time.Time{}
	}
	return time.Now().Add(fallbackWindow)
}

func nextRetryDelay(attempt int, cfg txRetryConfig, backoff time.Duration, deadline time.Time) (time.Duration, bool) {
	if cfg.maxAttempts > 0 && attempt >= cfg.maxAttempts {
		return 0, false
	}
	delay := backoff
	if cfg.jitter != nil {
		delay = cfg.jitter(delay)
	}
	if !deadline.IsZero() && time.Now().Add(delay).After(deadline) {
		return 0, false
	}
	return delay, true
}

func equalJitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	half := d / 2
	return half + rand.N(d-half+1)
}

func nextBackoff(current, maxBackoff time.Duration) time.Duration {
	next := current * 2
	if maxBackoff > 0 && next > maxBackoff {
		return maxBackoff
	}
	return next
}
