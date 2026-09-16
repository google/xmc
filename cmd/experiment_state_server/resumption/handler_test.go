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

package resumption

import (
	"context"
	"errors"
	"testing"

	"golang/cmp/cmp"
)

type mockWorker struct {
	resumeFunc func(ctx context.Context, req Request) error
	calls      []Request
}

func (m *mockWorker) Resume(ctx context.Context, req Request) error {
	m.calls = append(m.calls, req)
	if m.resumeFunc != nil {
		return m.resumeFunc(ctx, req)
	}
	return nil
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("nil worker returns error", func(t *testing.T) {
		t.Parallel()
		handler, err := NewHandler(nil)
		if err == nil {
			t.Fatal("NewHandler(nil) expected error, got nil")
		}
		if handler != nil {
			t.Fatalf("NewHandler(nil) expected nil handler, got %v", handler)
		}
	})

	t.Run("valid worker creates handler", func(t *testing.T) {
		t.Parallel()
		worker := &mockWorker{}
		handler, err := NewHandler(worker)
		if err != nil {
			t.Fatalf("NewHandler(worker) unexpected error: %v", err)
		}
		if handler == nil {
			t.Fatal("NewHandler(worker) expected non-nil handler, got nil")
		}
	})
}

func TestHandler_HandleTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success dispatch", func(t *testing.T) {
		t.Parallel()
		worker := &mockWorker{}
		handler, err := NewHandler(worker)
		if err != nil {
			t.Fatalf("NewHandler() error: %v", err)
		}

		payload := `{"resumption_run_id":"run-1","batch_size":200,"max_backlog_threshold":1000}`
		res := handler.HandleTask(ctx, "worker-1", payload)

		if !res.WasRunSuccessful {
			t.Errorf("WasRunSuccessful = false, want true")
		}
		if res.ShouldBeRetried {
			t.Errorf("ShouldBeRetried = true, want false")
		}
		if res.Error != nil {
			t.Errorf("Error = %v, want nil", res.Error)
		}

		wantCalls := []Request{
			{
				ResumptionRunID:     "run-1",
				BatchSize:           200,
				MaxBacklogThreshold: 1000,
			},
		}
		if diff := cmp.Diff(wantCalls, worker.calls); diff != "" {
			t.Errorf("worker.Resume calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("invalid json payload is not retried", func(t *testing.T) {
		t.Parallel()
		worker := &mockWorker{}
		handler, err := NewHandler(worker)
		if err != nil {
			t.Fatalf("NewHandler() error: %v", err)
		}

		res := handler.HandleTask(ctx, "worker-1", "{invalid json")

		if res.WasRunSuccessful {
			t.Errorf("WasRunSuccessful = true, want false")
		}
		if res.ShouldBeRetried {
			t.Errorf("ShouldBeRetried = true, want false (poison pill)")
		}
		if res.Error == nil {
			t.Errorf("Error = nil, want error")
		}
		if len(worker.calls) != 0 {
			t.Errorf("worker was called %d times, want 0", len(worker.calls))
		}
	})

	t.Run("missing run id is not retried", func(t *testing.T) {
		t.Parallel()
		worker := &mockWorker{}
		handler, err := NewHandler(worker)
		if err != nil {
			t.Fatalf("NewHandler() error: %v", err)
		}

		res := handler.HandleTask(ctx, "worker-1", `{"batch_size":100}`)

		if res.WasRunSuccessful {
			t.Errorf("WasRunSuccessful = true, want false")
		}
		if res.ShouldBeRetried {
			t.Errorf("ShouldBeRetried = true, want false")
		}
		if res.Error == nil {
			t.Errorf("Error = nil, want error")
		}
		if len(worker.calls) != 0 {
			t.Errorf("worker was called %d times, want 0", len(worker.calls))
		}
	})

	t.Run("worker error is retryable", func(t *testing.T) {
		t.Parallel()
		workerErr := errors.New("spanner transient error")
		worker := &mockWorker{
			resumeFunc: func(ctx context.Context, req Request) error {
				return workerErr
			},
		}
		handler, err := NewHandler(worker)
		if err != nil {
			t.Fatalf("NewHandler() error: %v", err)
		}

		payload := `{"resumption_run_id":"run-fail","batch_size":100,"max_backlog_threshold":500}`
		res := handler.HandleTask(ctx, "worker-1", payload)

		if res.WasRunSuccessful {
			t.Errorf("WasRunSuccessful = true, want false")
		}
		if !res.ShouldBeRetried {
			t.Errorf("ShouldBeRetried = false, want true")
		}
		if !errors.Is(res.Error, workerErr) {
			t.Errorf("Error = %v, want %v", res.Error, workerErr)
		}
	})
}
