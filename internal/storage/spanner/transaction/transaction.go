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

// Package transaction provides transaction context helpers and transaction runner for Cloud Spanner.
package transaction

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	"xmanager_cloud/internal/storage"
)

type contextKey struct{}

var transactionContextKey = contextKey{}

// WithReadWriteTransaction returns a new context with the provided read-write transaction attached.
func WithReadWriteTransaction(ctx context.Context, tx *spanner.ReadWriteTransaction) context.Context {
	return context.WithValue(ctx, transactionContextKey, tx)
}

// ReadWriteTransactionFromContext retrieves the read-write transaction from context, if present.
func ReadWriteTransactionFromContext(ctx context.Context) (*spanner.ReadWriteTransaction, bool) {
	if ctx == nil {
		return nil, false
	}
	tx, ok := ctx.Value(transactionContextKey).(*spanner.ReadWriteTransaction)
	return tx, ok && tx != nil
}

// RequireReadWriteTransaction retrieves the read-write transaction from context, or returns an error if not present.
func RequireReadWriteTransaction(ctx context.Context) (*spanner.ReadWriteTransaction, error) {
	tx, ok := ReadWriteTransactionFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("read-write transaction is required in context: %w", status.ErrFailedPrecondition)
	}
	return tx, nil
}

var _ storage.TransactionRunner = (*TransactionRunner)(nil)

// TransactionRunner executes operations within a Spanner read-write transaction.
type TransactionRunner struct {
	client *spanner.Client
}

// NewTransactionRunner creates a new TransactionRunner for the given Spanner client.
func NewTransactionRunner(client *spanner.Client) (*TransactionRunner, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", status.ErrInvalidArgument)
	}
	return &TransactionRunner{client: client}, nil
}

// RunInTransaction runs the provided function in a Spanner read-write transaction.
func (r *TransactionRunner) RunInTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if r.client == nil {
		return fmt.Errorf("spanner client must not be nil: %w", status.ErrFailedPrecondition)
	}
	if ctx == nil {
		return fmt.Errorf("context must not be nil: %w", status.ErrInvalidArgument)
	}
	if fn == nil {
		return fmt.Errorf("fn must not be nil: %w", status.ErrInvalidArgument)
	}
	if _, ok := ReadWriteTransactionFromContext(ctx); ok {
		return fmt.Errorf("nested transactions are not supported: %w", status.ErrFailedPrecondition)
	}
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		txCtx := WithReadWriteTransaction(ctx, tx)
		return fn(txCtx)
	})
	return err
}
