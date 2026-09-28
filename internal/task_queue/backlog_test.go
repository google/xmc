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

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
	"xmanager_cloud/kubernetes/common/constants"
)

type fakeRedisStreamClient struct {
	groupsResult    []redis.XInfoGroup
	groupsErr       error
	xrangeResult    []redis.XMessage
	xrangeErr       error
	recordedStream  string
	recordedStart   string
	recordedStop    string
	recordedCount   int64
	recordedInfoKey string
}

func (f *fakeRedisStreamClient) XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd {
	f.recordedInfoKey = key
	cmd := redis.NewXInfoGroupsCmd(ctx, key)
	if f.groupsErr != nil {
		cmd.SetErr(f.groupsErr)
	} else {
		cmd.SetVal(f.groupsResult)
	}
	return cmd
}

func (f *fakeRedisStreamClient) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	f.recordedStream = stream
	f.recordedStart = start
	f.recordedStop = stop
	f.recordedCount = count
	cmd := redis.NewXMessageSliceCmd(ctx, "xrange", stream, start, stop, "count", count)
	if f.xrangeErr != nil {
		cmd.SetErr(f.xrangeErr)
	} else {
		cmd.SetVal(f.xrangeResult)
	}
	return cmd
}

type fakePublisherWithoutStream struct{}

func (f *fakePublisherWithoutStream) AddMessage(ctx context.Context, data string, failedAttempts string) error {
	return nil
}
func (f *fakePublisherWithoutStream) Ping(ctx context.Context) error  { return nil }
func (f *fakePublisherWithoutStream) Close(ctx context.Context) error { return nil }

type fakePublisherWithStream struct {
	fakePublisherWithoutStream
	fakeRedisStreamClient
}

func TestCalculateStreamBacklog(t *testing.T) {
	ctx := context.Background()

	t.Run("nil client returns 0", func(t *testing.T) {
		backlog, err := CalculateStreamBacklog(ctx, nil, "stream", "group", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 0 {
			t.Errorf("backlog = %d, want 0", backlog)
		}
	})

	t.Run("non-positive threshold returns error", func(t *testing.T) {
		client := &fakeRedisStreamClient{}
		if _, err := CalculateStreamBacklog(ctx, client, "stream", "group", 0); err == nil {
			t.Fatal("expected error for zero threshold, got nil")
		}
	})

	for _, missingErr := range []error{redis.Nil, errors.New("ERR no such key")} {
		t.Run("missing stream returns 0: "+missingErr.Error(), func(t *testing.T) {
			client := &fakeRedisStreamClient{
				groupsErr: missingErr,
			}
			backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 100)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if backlog != 0 {
				t.Errorf("backlog = %d, want 0", backlog)
			}
		})
	}

	t.Run("groups error propagates", func(t *testing.T) {
		expectedErr := errors.New("redis connection failure")
		client := &fakeRedisStreamClient{
			groupsErr: expectedErr,
		}
		_, err := CalculateStreamBacklog(ctx, client, "stream", "group", 100)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("err = %v, want %v", err, expectedErr)
		}
	})

	t.Run("target group not found returns 0", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "other-group", Pending: 5, LastDeliveredID: "100-0"},
			},
		}
		backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 0 {
			t.Errorf("backlog = %d, want 0", backlog)
		}
	})

	t.Run("pending only with no undelivered lag", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "group", Pending: 7, LastDeliveredID: "100-0"},
			},
			xrangeResult: []redis.XMessage{
				{ID: "100-0"}, // Matches lastDeliveredID, stripped.
			},
		}
		backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 7 {
			t.Errorf("backlog = %d, want 7", backlog)
		}
		if client.recordedStart != "100-0" {
			t.Errorf("start = %q, want %q", client.recordedStart, "100-0")
		}
		if client.recordedCount != 94 {
			t.Errorf("count = %d, want 94 (threshold 100 - pending 7 + 1)", client.recordedCount)
		}
	})

	t.Run("pending meets threshold skips xrange", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "group", Pending: 12, LastDeliveredID: "100-0"},
			},
			xrangeErr: errors.New("xrange must not be called"),
		}
		backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 12 {
			t.Errorf("backlog = %d, want 12", backlog)
		}
		if client.recordedStream != "" {
			t.Errorf("xrange was called on stream %q, want no call", client.recordedStream)
		}
	})

	t.Run("pending and lag combined", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "group", Pending: 3, LastDeliveredID: "100-0"},
			},
			xrangeResult: []redis.XMessage{
				{ID: "100-0"}, // Matches lastDeliveredID, stripped.
				{ID: "101-0"},
				{ID: "102-0"},
			},
		}
		backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 5 {
			t.Errorf("backlog = %d, want 5 (3 pending + 2 lag)", backlog)
		}
		if client.recordedCount != 8 {
			t.Errorf("count = %d, want 8 (threshold 10 - pending 3 + 1)", client.recordedCount)
		}
	})

	t.Run("initial stream state without delivered messages", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "group", Pending: 0, LastDeliveredID: ""},
			},
			xrangeResult: []redis.XMessage{
				{ID: "1-0"},
				{ID: "2-0"},
			},
		}
		backlog, err := CalculateStreamBacklog(ctx, client, "stream", "group", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 2 {
			t.Errorf("backlog = %d, want 2", backlog)
		}
		if client.recordedStart != "-" {
			t.Errorf("start = %q, want %q", client.recordedStart, "-")
		}
	})

	t.Run("xrange error propagates", func(t *testing.T) {
		expectedErr := errors.New("xrange failure")
		client := &fakeRedisStreamClient{
			groupsResult: []redis.XInfoGroup{
				{Name: "group", Pending: 3, LastDeliveredID: "100-0"},
			},
			xrangeErr: expectedErr,
		}
		_, err := CalculateStreamBacklog(ctx, client, "stream", "group", 10)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("err = %v, want %v", err, expectedErr)
		}
	})
}

func TestClient_CalculateBacklog(t *testing.T) {
	ctx := context.Background()

	t.Run("nil redisClient returns 0", func(t *testing.T) {
		c := &Client{}
		backlog, err := c.CalculateBacklog(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 0 {
			t.Errorf("backlog = %d, want 0", backlog)
		}
	})

	t.Run("redisClient not implementing RedisStreamClient returns 0", func(t *testing.T) {
		c := &Client{
			redisClient: &fakePublisherWithoutStream{},
		}
		backlog, err := c.CalculateBacklog(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 0 {
			t.Errorf("backlog = %d, want 0", backlog)
		}
	})

	t.Run("valid stream client calculates correctly", func(t *testing.T) {
		pub := &fakePublisherWithStream{
			fakeRedisStreamClient: fakeRedisStreamClient{
				groupsResult: []redis.XInfoGroup{
					{Name: constants.RedisGroup, Pending: 4, LastDeliveredID: "50-0"},
				},
				xrangeResult: []redis.XMessage{
					{ID: "50-0"},
					{ID: "51-0"},
				},
			},
		}
		c := &Client{
			redisClient: pub,
		}
		backlog, err := c.CalculateBacklog(ctx, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backlog != 5 {
			t.Errorf("backlog = %d, want 5 (4 pending + 1 lag)", backlog)
		}
		if pub.recordedInfoKey != constants.RedisStream {
			t.Errorf("queried key = %q, want %q", pub.recordedInfoKey, constants.RedisStream)
		}
	})
}
