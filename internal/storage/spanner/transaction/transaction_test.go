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

package transaction

import (
	"context"
	"errors"
	"testing"

	"task/go/status"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestTransactionContext(t *testing.T) {
	ctx := context.Background()

	// Initially no transaction in context.
	if tx, ok := ReadWriteTransactionFromContext(ctx); ok || tx != nil {
		t.Errorf("expected no transaction in fresh context, got %v", tx)
	}
	if _, err := RequireReadWriteTransaction(ctx); !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("RequireReadWriteTransaction returned error %v, want FailedPrecondition", err)
	}

	// Test nil context
	if tx, ok := ReadWriteTransactionFromContext(nil); ok || tx != nil {
		t.Errorf("expected false for nil context, got tx=%v, ok=%v", tx, ok)
	}
}

func TestNewTransactionRunner_NilClient(t *testing.T) {
	runner, err := NewTransactionRunner(nil)
	if err == nil {
		t.Errorf("expected error when client is nil, got %v", runner)
	}
}

func TestTransactionRunner_RunInTransaction(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)

	runner, err := NewTransactionRunner(client)
	if err != nil {
		t.Fatalf("NewTransactionRunner failed: %v", err)
	}

	ran := false
	err = runner.RunInTransaction(ctx, func(txCtx context.Context) error {
		tx, err := RequireReadWriteTransaction(txCtx)
		if err != nil {
			t.Fatalf("RequireReadWriteTransaction failed: %v", err)
		}
		if tx == nil {
			t.Fatal("expected non-nil transaction in txCtx")
		}
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction failed: %v", err)
	}
	if !ran {
		t.Error("expected transaction function to be executed")
	}
}

func TestTransactionRunner_RunInTransaction_NilArgs(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)

	runner, err := NewTransactionRunner(client)
	if err != nil {
		t.Fatalf("NewTransactionRunner failed: %v", err)
	}

	if err := runner.RunInTransaction(nil, func(txCtx context.Context) error { return nil }); !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("RunInTransaction(nil ctx) returned %v, want ErrInvalidArgument", err)
	}
	if err := runner.RunInTransaction(ctx, nil); !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("RunInTransaction(nil fn) returned %v, want ErrInvalidArgument", err)
	}
}

func TestTransactionRunner_RunInTransaction_ErrorRollback(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)

	runner, err := NewTransactionRunner(client)
	if err != nil {
		t.Fatalf("NewTransactionRunner failed: %v", err)
	}

	expectedErr := errors.New("simulated business error")
	err = runner.RunInTransaction(ctx, func(txCtx context.Context) error {
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("RunInTransaction returned %v, want %v", err, expectedErr)
	}
}

func TestTransactionRunner_RunInTransaction_NestedNotSupported(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)

	runner, err := NewTransactionRunner(client)
	if err != nil {
		t.Fatalf("NewTransactionRunner failed: %v", err)
	}

	err = runner.RunInTransaction(ctx, func(txCtx context.Context) error {
		return runner.RunInTransaction(txCtx, func(nestedCtx context.Context) error {
			return nil
		})
	})
	if !errors.Is(err, status.ErrFailedPrecondition) {
		t.Errorf("nested RunInTransaction returned %v, want FailedPrecondition", err)
	}
}
