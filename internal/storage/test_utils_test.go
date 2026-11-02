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

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockStorage_RunInSerializableTransaction(t *testing.T) {
	ctx := context.Background()
	fixedTime := time.Date(2026, time.October, 29, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("tx failed")

	t.Run("returns configured CommitTime", func(t *testing.T) {
		m := &MockStorage{CommitTime: fixedTime}
		called := false
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("RunInSerializableTransaction() error = %v", err)
		}
		if !called {
			t.Error("RunInSerializableTransaction() did not invoke callback")
		}
		if !got.Equal(fixedTime) {
			t.Errorf("CommitTime = %v, want %v", got, fixedTime)
		}
	})

	t.Run("defaults to current time when CommitTime is zero", func(t *testing.T) {
		before := time.Now()
		m := &MockStorage{}
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			return nil
		})
		after := time.Now()
		if err != nil {
			t.Fatalf("RunInSerializableTransaction() error = %v", err)
		}
		if got.Before(before) || got.After(after) {
			t.Errorf("CommitTime = %v, want within [%v, %v]", got, before, after)
		}
	})

	t.Run("propagates error with zero timestamp", func(t *testing.T) {
		m := &MockStorage{CommitTime: fixedTime, RunInTransactionErr: wantErr}
		got, err := m.RunInSerializableTransaction(ctx, func(context.Context, *Transaction) error {
			return nil
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("RunInSerializableTransaction() error = %v, want %v", err, wantErr)
		}
		if !got.IsZero() {
			t.Errorf("CommitTime = %v, want zero time on error", got)
		}
	})
}
