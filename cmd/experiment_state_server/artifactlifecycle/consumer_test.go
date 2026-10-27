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

package artifactlifecycle

import (
	"context"
	"testing"
	"time"

	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

type mockRedisConsumer struct {
	redisclient.Consumer
	initialized bool
	started     bool
	cleaned     bool
	closed      bool
	taskHandler func(ctx context.Context, workerID string, data string) redisclient.TaskResult
}

func (m *mockRedisConsumer) InitializeClient(ctx context.Context) {
	m.initialized = true
}

func (m *mockRedisConsumer) SetTaskHandler(handler func(ctx context.Context, workerID string, data string) redisclient.TaskResult) {
	m.taskHandler = handler
}

func (m *mockRedisConsumer) StartWorker(ctx context.Context, workerID string) {
	m.started = true
	<-ctx.Done()
}

func (m *mockRedisConsumer) RunCleanup(ctx context.Context, cleanupWorkerID string) {
	m.cleaned = true
	<-ctx.Done()
}

func (m *mockRedisConsumer) Close(ctx context.Context) error {
	m.closed = true
	return nil
}

func TestStartConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockClient := &mockRedisConsumer{}
	reg := &mockLifecycleRegistry{}
	worker := NewWorker(nil, reg)

	consumer, err := StartConsumer(ctx, ConsumerConfig{
		Client: mockClient,
	}, worker)
	if err != nil {
		t.Fatalf("StartConsumer failed: %v", err)
	}

	if !mockClient.initialized {
		t.Errorf("expected client to be initialized")
	}
	if mockClient.taskHandler == nil {
		t.Errorf("expected task handler to be set")
	}

	// Give goroutines time to run
	time.Sleep(20 * time.Millisecond)
	if !mockClient.started {
		t.Errorf("expected worker to be started")
	}
	if !mockClient.cleaned {
		t.Errorf("expected cleanup to be started")
	}

	if err := consumer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !mockClient.closed {
		t.Errorf("expected client to be closed")
	}
}
