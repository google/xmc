// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

// fakeConsumer is a redisclient.Consumer whose loops block until their context is done, or until
// release is closed when ignoreCancel is set. Each loop sends its ID on started when it begins.
type fakeConsumer struct {
	redisclient.Consumer
	ignoreCancel bool
	release      chan struct{}
	started      chan string
	closeErr     error

	mu         sync.Mutex
	handler    func(context.Context, string, string) redisclient.TaskResult
	stopped    int
	closeCalls int
}

func newFakeConsumer() *fakeConsumer {
	return &fakeConsumer{release: make(chan struct{}), started: make(chan string, 2)}
}

func (f *fakeConsumer) SetTaskHandler(h func(context.Context, string, string) redisclient.TaskResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = h
}

func (f *fakeConsumer) StartWorker(ctx context.Context, workerID string) { f.loop(ctx, workerID) }

func (f *fakeConsumer) RunCleanup(ctx context.Context, workerID string) { f.loop(ctx, workerID) }

func (f *fakeConsumer) loop(ctx context.Context, id string) {
	f.started <- id
	if f.ignoreCancel {
		<-f.release
	} else {
		<-ctx.Done()
	}
	f.mu.Lock()
	f.stopped++
	f.mu.Unlock()
}

func (f *fakeConsumer) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

// waitForLoops returns the IDs of both loops once they have started, failing the test after a
// deadline.
func (f *fakeConsumer) waitForLoops(t *testing.T) []string {
	t.Helper()
	var ids []string
	deadline := time.After(5 * time.Second)
	for len(ids) < 2 {
		select {
		case id := <-f.started:
			ids = append(ids, id)
		case <-deadline:
			t.Fatalf("consumer loops did not start before the deadline; started %v", ids)
		}
	}
	return ids
}

func TestStart_RunsLoopsWithHandleTask(t *testing.T) {
	fake := newFakeConsumer()
	c := Start(context.Background(), fake)
	defer c.Close(context.Background())
	ids := fake.waitForLoops(t)

	fake.mu.Lock()
	handler := fake.handler
	fake.mu.Unlock()
	for _, prefix := range []string{constants.StudyControllerWorkerID + "-", constants.StudyControllerCleanupWorkerID + "-"} {
		if !containsPrefix(ids, prefix) {
			t.Errorf("loop IDs = %v, want one with prefix %q", ids, prefix)
		}
	}
	if handler == nil {
		t.Fatal("Start() did not set a task handler")
	}
	if got := handler(context.Background(), "w", `{"experiment":"experiments/7"}`); !got.WasRunSuccessful {
		t.Errorf("task handler on a valid tick = %+v, want success (handleTask)", got)
	}
}

func containsPrefix(ids []string, prefix string) bool {
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func TestConsumer_CloseStopsLoopsAndClosesClient(t *testing.T) {
	fake := newFakeConsumer()
	fake.closeErr = errors.New("close failed")
	c := Start(context.Background(), fake)
	fake.waitForLoops(t)

	if err := c.Close(context.Background()); !errors.Is(err, fake.closeErr) {
		t.Errorf("Close() error = %v, want %v", err, fake.closeErr)
	}
	if err := c.Close(context.Background()); !errors.Is(err, fake.closeErr) {
		t.Errorf("second Close() error = %v, want the first call's %v", err, fake.closeErr)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.stopped != 2 || fake.closeCalls != 1 {
		t.Errorf("after Close: %d loops stopped, %d client closes; want 2 and 1", fake.stopped, fake.closeCalls)
	}
}

func TestStart_ContextCancelStopsLoops(t *testing.T) {
	fake := newFakeConsumer()
	ctx, cancel := context.WithCancel(context.Background())
	c := Start(ctx, fake)
	fake.waitForLoops(t)

	cancel()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loops did not stop after the context was cancelled")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestConsumer_CloseWithStuckLoops(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name         string
		ctx          context.Context
		drainTimeout time.Duration
		minElapsed   time.Duration
	}{
		{name: "drain timeout", ctx: context.Background(), drainTimeout: 20 * time.Millisecond, minElapsed: 20 * time.Millisecond},
		{name: "context done", ctx: cancelled, drainTimeout: time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeConsumer()
			fake.ignoreCancel = true
			defer close(fake.release)
			c := Start(context.Background(), fake)
			fake.waitForLoops(t)
			c.drainTimeout = tc.drainTimeout

			start := time.Now()
			if err := c.Close(tc.ctx); err != nil {
				t.Errorf("Close() error = %v, want nil", err)
			}
			if elapsed := time.Since(start); elapsed < tc.minElapsed || elapsed > time.Second {
				t.Errorf("Close() with stuck loops took %v, want between %v and 1s", elapsed, tc.minElapsed)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.closeCalls != 1 {
				t.Errorf("client closes = %d, want 1 even when loops are stuck", fake.closeCalls)
			}
		})
	}
}

// consumerSettings is the part of a ConsumerClient that NewRedisConsumer configures.
type consumerSettings struct {
	Addr, Stream, Group, DLQ string
	MaxRetries               int
	ContextTimeoutEnabled    bool
}

func TestNewRedisConsumer(t *testing.T) {
	c := NewRedisConsumer("redis-service:6379")
	defer c.Close(context.Background())
	client, ok := c.Client.(*redis.Client)
	if !ok {
		t.Fatalf("NewRedisConsumer() client type = %T, want *redis.Client", c.Client)
	}

	got := consumerSettings{
		Addr: client.Options().Addr, Stream: c.StreamName, Group: c.GroupName, DLQ: c.DLQStreamName,
		MaxRetries: c.MaxRetries, ContextTimeoutEnabled: client.Options().ContextTimeoutEnabled,
	}
	want := consumerSettings{
		Addr: "redis-service:6379", Stream: "xmc-study-reconcile-stream", Group: "study-controller-group",
		DLQ: "xmc-study-reconcile-dlq-stream", MaxRetries: constants.RedisMaxRetries, ContextTimeoutEnabled: true,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NewRedisConsumer() settings mismatch (-want +got):\n%s", diff)
	}
}
