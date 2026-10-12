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
	"strings"
	"testing"
	"time"

	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

func TestStartConsumer_Validation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("nil worker returns error", func(t *testing.T) {
		t.Parallel()
		_, err := StartConsumer(ctx, ConsumerConfig{RedisAddr: "localhost:6379"}, nil)
		if err == nil {
			t.Fatal("StartConsumer with nil worker want error, got nil")
		}
		if !strings.Contains(err.Error(), "resumption worker cannot be nil") {
			t.Errorf("StartConsumer error = %q, want substring 'resumption worker cannot be nil'", err.Error())
		}
	})

	t.Run("empty RedisAddr returns error", func(t *testing.T) {
		t.Parallel()
		_, err := StartConsumer(ctx, ConsumerConfig{RedisAddr: ""}, NewResumptionWorker(nil, nil, nil))
		if err == nil {
			t.Fatal("StartConsumer with empty RedisAddr want error, got nil")
		}
		if !strings.Contains(err.Error(), "redis address cannot be empty") {
			t.Errorf("StartConsumer error = %q, want substring 'redis address cannot be empty'", err.Error())
		}
	})
}

func TestStartConsumer_SuccessWithFakeClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fakeClient := redisclient.NewFakeClient()

	consumer, err := StartConsumer(ctx, ConsumerConfig{Client: fakeClient}, NewResumptionWorker(nil, nil, nil))
	if err != nil {
		t.Fatalf("StartConsumer with fake client failed: %v", err)
	}

	if err := consumer.Close(); err != nil {
		t.Errorf("consumer.Close() = %v, want nil", err)
	}
}

func TestConsumer_CloseNil(t *testing.T) {
	t.Parallel()

	var c *Consumer
	if err := c.Close(); err != nil {
		t.Errorf("nil Consumer.Close() = %v, want nil", err)
	}
}

func TestConsumer_CloseIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fakeClient := redisclient.NewFakeClient()

	consumer, err := StartConsumer(ctx, ConsumerConfig{Client: fakeClient}, NewResumptionWorker(nil, nil, nil))
	if err != nil {
		t.Fatalf("StartConsumer failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := consumer.Close(); err != nil {
			t.Errorf("iteration %d: consumer.Close() = %v, want nil", i, err)
		}
	}
}

func TestConsumer_DrainTimeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fakeClient := redisclient.NewFakeClient()

	consumer, err := StartConsumer(ctx, ConsumerConfig{Client: fakeClient}, NewResumptionWorker(nil, nil, nil))
	if err != nil {
		t.Fatalf("StartConsumer failed: %v", err)
	}

	// Simulate an in-flight worker that does not complete
	consumer.wg.Add(1)
	defer consumer.wg.Done()
	consumer.drainTimeout = 15 * time.Millisecond

	start := time.Now()
	if err := consumer.Close(); err != nil {
		t.Errorf("consumer.Close() = %v, want nil", err)
	}
	elapsed := time.Since(start)

	if elapsed < 10*time.Millisecond {
		t.Errorf("Close() elapsed = %v, want >= 10ms for drain timeout", elapsed)
	}
	if elapsed > 1*time.Second {
		t.Errorf("Close() elapsed = %v, want < 1s (drain timeout should be bounded)", elapsed)
	}
}

func TestEssWorkerID(t *testing.T) {
	t.Parallel()

	id := essWorkerID("test-prefix")
	if !strings.HasPrefix(id, "test-prefix-") {
		t.Errorf("essWorkerID(\"test-prefix\") = %q, want prefix \"test-prefix-\"", id)
	}
}
