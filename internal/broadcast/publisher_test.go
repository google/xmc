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

package broadcast

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockRedisPublisher struct {
	xadd func(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
}

func (m *mockRedisPublisher) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	return m.xadd(ctx, a)
}

func (m *mockRedisPublisher) Close() error { return nil }

func TestNewPublisher(t *testing.T) {
	var pub Publisher = NewPublisher(nil, "test-stream")
	if pub == nil {
		t.Fatal("NewPublisher() returned nil")
	}
}

func TestPublisherClientValidation(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	dummyClient := &mockRedisPublisher{}

	tests := []struct {
		name       string
		client     redisPublisherClient
		streamName string
		ctx        context.Context
		wantErr    error
		errMsg     string
	}{
		{name: "nil redis client", client: nil, streamName: "test-stream", ctx: context.Background(), errMsg: "redis client is not initialized"},
		{name: "empty stream name", client: dummyClient, streamName: "", ctx: context.Background(), errMsg: "stream name is empty"},
		{name: "canceled context", client: dummyClient, streamName: "test-stream", ctx: canceledCtx, wantErr: context.Canceled},
		{name: "deadline exceeded context", client: dummyClient, streamName: "test-stream", ctx: deadlineCtx, wantErr: context.DeadlineExceeded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pub := &PublisherClient{client: tc.client, streamName: tc.streamName}
			err := pub.Publish(tc.ctx, "msg")
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("Publish() error = %v, want %v", err, tc.wantErr)
			}
			if tc.errMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.errMsg)) {
				t.Errorf("Publish() error = %v, want containing %q", err, tc.errMsg)
			}
		})
	}
}

func TestPublisherClientPublish(t *testing.T) {
	t.Run("success passes correct arguments", func(t *testing.T) {
		var recordedArgs *redis.XAddArgs
		mock := &mockRedisPublisher{
			xadd: func(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
				recordedArgs = a
				return redis.NewStringResult("1-0", nil)
			},
		}

		pub := &PublisherClient{client: mock, streamName: "xmc-pause-stream"}
		if err := pub.Publish(context.Background(), `{"event":"SchedulingPaused"}`); err != nil {
			t.Fatalf("Publish() unexpected error: %v", err)
		}

		if recordedArgs.Stream != "xmc-pause-stream" {
			t.Errorf("Stream = %q, want %q", recordedArgs.Stream, "xmc-pause-stream")
		}
		if recordedArgs.MaxLen != DefaultMaxLen || !recordedArgs.Approx {
			t.Errorf("MaxLen = %d, Approx = %v, want %d, true", recordedArgs.MaxLen, recordedArgs.Approx, DefaultMaxLen)
		}
		vals, ok := recordedArgs.Values.(map[string]any)
		if !ok || vals["message"] != `{"event":"SchedulingPaused"}` {
			t.Errorf("Values = %v, want message payload", recordedArgs.Values)
		}
	})

	t.Run("redis error wrapped", func(t *testing.T) {
		mock := &mockRedisPublisher{
			xadd: func(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
				return redis.NewStringResult("", errors.New("simulated redis error"))
			},
		}

		pub := &PublisherClient{client: mock, streamName: "xmc-pause-stream"}
		err := pub.Publish(context.Background(), "payload")
		if err == nil || !strings.Contains(err.Error(), "xmc-pause-stream") || errors.Unwrap(err) == nil {
			t.Errorf("Publish() error = %v, want wrapped error mentioning stream", err)
		}
	})

	t.Run("close nil or open client", func(t *testing.T) {
		if err := (&PublisherClient{}).Close(); err != nil {
			t.Errorf("Close() on nil client = %v, want nil", err)
		}
		if err := (&PublisherClient{client: &mockRedisPublisher{}}).Close(); err != nil {
			t.Errorf("Close() on mock client = %v, want nil", err)
		}
	})
}
