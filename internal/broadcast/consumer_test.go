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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockRedisConsumer struct {
	xrevrange func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	xread     func(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
}

func (m *mockRedisConsumer) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	if m.xrevrange != nil {
		return m.xrevrange(ctx, stream, start, stop, count)
	}
	return redis.NewXMessageSliceCmdResult(nil, nil)
}

func (m *mockRedisConsumer) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	if m.xread != nil {
		return m.xread(ctx, a)
	}
	return redis.NewXStreamSliceCmdResult(nil, nil)
}

func (m *mockRedisConsumer) Close() error { return nil }

func TestNewConsumer(t *testing.T) {
	c := NewConsumer(nil, "test-stream")
	if c == nil || c.streamName != "test-stream" || c.blockTimeout != DefaultBlockTimeout {
		t.Fatalf("NewConsumer() unexpected state: %+v", c)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() on nil client = %v, want nil", err)
	}
	if err := (&ConsumerClient{client: &mockRedisConsumer{}}).Close(); err != nil {
		t.Errorf("Close() on mock client = %v, want nil", err)
	}
}

func TestConsumerClientValidation(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	dummyClient := &mockRedisConsumer{}

	tests := []struct {
		name    string
		client  redisConsumerClient
		stream  string
		ctx     context.Context
		handler func(string)
		wantErr error
		errMsg  string
	}{
		{name: "nil handler", client: nil, stream: "s", ctx: context.Background(), handler: nil},
		{name: "nil client", client: nil, stream: "s", ctx: context.Background(), handler: func(string) {}, errMsg: "redis client is not initialized"},
		{name: "empty stream", client: dummyClient, stream: "", ctx: context.Background(), handler: func(string) {}, errMsg: "stream name is empty"},
		{name: "canceled ctx", client: dummyClient, stream: "s", ctx: canceledCtx, handler: func(string) {}},
		{name: "deadline exceeded", client: dummyClient, stream: "s", ctx: deadlineCtx, handler: func(string) {}, wantErr: context.DeadlineExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&ConsumerClient{client: tc.client, streamName: tc.stream}).Subscribe(tc.ctx, "$", tc.handler)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("Subscribe() error = %v, want %v", err, tc.wantErr)
			}
			if tc.errMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.errMsg)) {
				t.Errorf("Subscribe() error = %v, want containing %q", err, tc.errMsg)
			}
		})
	}
}

func TestConsumerClientLatestOffset(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	dummyClient := &mockRedisConsumer{}

	tests := []struct {
		name      string
		client    redisConsumerClient
		stream    string
		ctx       context.Context
		xrevrange func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
		want      string
		wantErr   error
		errMsg    string
	}{
		{name: "nil client", client: nil, stream: "s", ctx: context.Background(), errMsg: "redis client is not initialized"},
		{name: "empty stream", client: dummyClient, stream: "", ctx: context.Background(), errMsg: "stream name is empty"},
		{name: "canceled ctx", client: dummyClient, stream: "s", ctx: canceledCtx, wantErr: context.Canceled},
		{
			name:   "empty stream default 0",
			client: dummyClient,
			stream: "s",
			ctx:    context.Background(),
			xrevrange: func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
				return redis.NewXMessageSliceCmdResult(nil, nil)
			},
			want: "0",
		},
		{
			name:   "redis nil default 0",
			client: dummyClient,
			stream: "s",
			ctx:    context.Background(),
			xrevrange: func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
				return redis.NewXMessageSliceCmdResult(nil, redis.Nil)
			},
			want: "0",
		},
		{
			name:   "non-empty stream returns latest ID",
			client: dummyClient,
			stream: "s",
			ctx:    context.Background(),
			xrevrange: func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
				if stream != "s" || start != "+" || stop != "-" || count != 1 {
					t.Errorf("unexpected XRevRange args: %s, %s, %s, %d", stream, start, stop, count)
				}
				return redis.NewXMessageSliceCmdResult([]redis.XMessage{{ID: "1710000000000-0"}}, nil)
			},
			want: "1710000000000-0",
		},
		{
			name:   "error wrapped",
			client: dummyClient,
			stream: "s",
			ctx:    context.Background(),
			xrevrange: func(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
				return redis.NewXMessageSliceCmdResult(nil, errors.New("connection reset"))
			},
			errMsg: "failed to query latest offset for stream \"s\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.client
			if tc.xrevrange != nil {
				client = &mockRedisConsumer{xrevrange: tc.xrevrange}
			}
			offset, err := (&ConsumerClient{client: client, streamName: tc.stream}).LatestOffset(tc.ctx)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("LatestOffset() err = %v, want %v", err, tc.wantErr)
			}
			if tc.errMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.errMsg)) {
				t.Errorf("LatestOffset() err = %v, want containing %q", err, tc.errMsg)
			}
			if tc.want != "" && offset != tc.want {
				t.Errorf("LatestOffset() offset = %q, want %q", offset, tc.want)
			}
		})
	}
}

func TestConsumerClientSubscribe(t *testing.T) {
	t.Run("advances offset and dispatches string and bytes", func(t *testing.T) {
		var readCount int
		var recordedOffsets []string

		mock := &mockRedisConsumer{
			xread: func(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
				readCount++
				recordedOffsets = append(recordedOffsets, a.Streams[1])
				switch readCount {
				case 1:
					// redis.Nil triggers block timeout retry loop
					return redis.NewXStreamSliceCmdResult(nil, redis.Nil)
				case 2:
					return redis.NewXStreamSliceCmdResult([]redis.XStream{
						{
							Stream: "xmc-pause-stream",
							Messages: []redis.XMessage{
								{ID: "100-0", Values: map[string]any{"message": "hello-string"}},
								{ID: "101-0", Values: map[string]any{"message": []byte("hello-bytes"), "ignored": 123}},
							},
						},
					}, nil)
				default:
					return redis.NewXStreamSliceCmdResult(nil, errors.New("stop loop"))
				}
			},
		}

		c := &ConsumerClient{
			client:       mock,
			streamName:   "xmc-pause-stream",
			blockTimeout: 10 * time.Millisecond,
		}

		var received []string
		err := c.Subscribe(context.Background(), "", func(msg string) {
			received = append(received, msg)
		})
		if err == nil || !strings.Contains(err.Error(), "stop loop") {
			t.Fatalf("Subscribe() error = %v, want containing 'stop loop'", err)
		}

		if want := []string{"hello-string", "hello-bytes"}; !slices.Equal(received, want) {
			t.Errorf("received = %v, want %v", received, want)
		}
		if len(recordedOffsets) < 3 {
			t.Fatalf("expected at least 3 XRead calls, got %d", len(recordedOffsets))
		}
		if recordedOffsets[0] != "$" {
			t.Errorf("initial offset = %q, want '$'", recordedOffsets[0])
		}
		if recordedOffsets[2] != "101-0" {
			t.Errorf("advanced offset = %q, want '101-0'", recordedOffsets[2])
		}
	})

	t.Run("stream read error wrapped", func(t *testing.T) {
		mock := &mockRedisConsumer{
			xread: func(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
				return redis.NewXStreamSliceCmdResult(nil, errors.New("read tcp error"))
			},
		}
		c := &ConsumerClient{client: mock, streamName: "xmc-pause-stream"}
		err := c.Subscribe(context.Background(), "$", func(string) {})
		if err == nil || !strings.Contains(err.Error(), "failed to read from broadcast stream") {
			t.Errorf("Subscribe() error = %v, want containing 'failed to read from broadcast stream'", err)
		}
	})

	t.Run("context deadline returns deadline error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		mock := &mockRedisConsumer{
			xread: func(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
				<-ctx.Done()
				return redis.NewXStreamSliceCmdResult(nil, ctx.Err())
			},
		}
		c := &ConsumerClient{client: mock, streamName: "xmc-pause-stream"}
		if err := c.Subscribe(ctx, "$", func(string) {}); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Subscribe() error = %v, want context.DeadlineExceeded", err)
		}
	})
}
