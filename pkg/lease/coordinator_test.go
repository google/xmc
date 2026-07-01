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

package lease

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockRedis struct {
	redis.Cmdable
	mu           sync.Mutex
	store        map[string]string
	expireCalled map[string]time.Duration
	setNXErr     error
	getErr       error
}

func newMockRedis() *mockRedis {
	return &mockRedis{
		store:        make(map[string]string),
		expireCalled: make(map[string]time.Duration),
	}
}

func (m *mockRedis) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewBoolCmd(ctx)
	if m.setNXErr != nil {
		cmd.SetErr(m.setNXErr)
		return cmd
	}

	valStr := value.(string)
	if _, ok := m.store[key]; ok {
		cmd.SetVal(false)
		return cmd
	}
	m.store[key] = valStr
	cmd.SetVal(true)
	return cmd
}

func (m *mockRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewStringCmd(ctx)
	if m.getErr != nil {
		cmd.SetErr(m.getErr)
		return cmd
	}

	val, ok := m.store[key]
	if !ok {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(val)
	return cmd
}

func (m *mockRedis) Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewBoolCmd(ctx)
	if _, ok := m.store[key]; !ok {
		cmd.SetVal(false)
		return cmd
	}
	m.expireCalled[key] = expiration
	cmd.SetVal(true)
	return cmd
}

func (m *mockRedis) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewCmd(ctx)
	key := keys[0]
	identity := args[0].(string)

	if m.store[key] == identity {
		delete(m.store, key)
		cmd.SetVal(int64(1))
	} else {
		cmd.SetVal(int64(0))
	}
	return cmd
}

func TestCoordinator_LeadershipTransitions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mr := newMockRedis()
	c := NewCoordinator(mr, "test-lock", "replica-1", 50*time.Millisecond, 10*time.Millisecond)

	var startCallCount int
	var stopCallCount int
	var mu sync.Mutex

	startWorker := func(ctx context.Context) {
		mu.Lock()
		startCallCount++
		mu.Unlock()
	}

	stopWorker := func() {
		mu.Lock()
		stopCallCount++
		mu.Unlock()
	}

	// Start coordinator in background
	go c.Run(ctx, startWorker, stopWorker)

	// Wait a bit to let it acquire leadership
	time.Sleep(35 * time.Millisecond)

	mu.Lock()
	if startCallCount != 1 {
		t.Errorf("Expected startWorker to be called 1 time, got %d", startCallCount)
	}
	if stopCallCount != 0 {
		t.Errorf("Expected stopWorker to be called 0 times, got %d", stopCallCount)
	}
	mu.Unlock()

	// Simulate lock expiration/takeover by another replica
	mr.mu.Lock()
	mr.store["test-lock"] = "replica-2"
	mr.mu.Unlock()

	// Wait for coordinator check
	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	if stopCallCount != 1 {
		t.Errorf("Expected stopWorker to be called 1 time after losing leadership, got %d", stopCallCount)
	}
	mu.Unlock()

	// Re-acquire lock
	mr.mu.Lock()
	delete(mr.store, "test-lock")
	mr.mu.Unlock()

	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	if startCallCount != 2 {
		t.Errorf("Expected startWorker to be called 2 times after re-winning, got %d", startCallCount)
	}
	mu.Unlock()

	// Cancel context to stop coordinator
	cancel()
	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	if stopCallCount != 2 {
		t.Errorf("Expected stopWorker to be called 2 times after termination, got %d", stopCallCount)
	}
	mu.Unlock()
}
