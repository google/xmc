// Copyright 2025 Google LLC
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
// Package redisclient is a prototype implementation of the message queueing system that is meant
// to replace Cloud Tasks in the current design.
package redisclient

import (
	"context"
	"fmt"
	"os"
	"time"

	"xmanager_cloud/internal/logging"

	"github.com/redis/go-redis/v9"
)

// TaskResult is the return type of TaskHandler.
type TaskResult struct {
	// Whether the task was run successfully.
	WasRunSuccessful bool
	// Whether the task should be retried.
	ShouldBeRetried bool
	Error           error
}

// Config holds all of the necessary pieces to connect to Redis instance.
type Config struct {
	Addr string
}

// Publisher is an interface for publishing messages to Redis.
type Publisher interface {
	AddMessage(ctx context.Context, data string, failedAttempts string) error
	// BacklogLength returns the number of unprocessed messages for the consumer group on the
	// publisher's stream, exact below maxThreshold and capped just above it.
	BacklogLength(ctx context.Context, group string, maxThreshold int) (int64, error)
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// Consumer is an interface for consuming messages from Redis.
type Consumer interface {
	InitializeClient(ctx context.Context)
	StartWorker(ctx context.Context, workerID string)
	RunCleanup(ctx context.Context, workerID string)
	SetTaskHandler(taskHandler func(ctx context.Context, workerID string, data string) TaskResult)
	ExtendClaim(ctx context.Context) error
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// Client is the subset of go-redis commands used by this package. *redis.Client satisfies it.
type Client interface {
	Ping(ctx context.Context) *redis.StatusCmd
	Close() error
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	FlushAll(ctx context.Context) *redis.StatusCmd
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	XClaim(ctx context.Context, a *redis.XClaimArgs) *redis.XMessageSliceCmd
	XAutoClaim(ctx context.Context, a *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
	XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XPendingExt(ctx context.Context, a *redis.XPendingExtArgs) *redis.XPendingExtCmd
	XLen(ctx context.Context, stream string) *redis.IntCmd
	XRange(ctx context.Context, stream, start, stop string) *redis.XMessageSliceCmd
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd
}

type baseManager struct {
	Client
	StreamName string
}

// PublisherClient is an extension of baseManager that allows publishing messages to Redis.
type PublisherClient struct {
	baseManager
}

// ConsumerClient is an extension of baseManager that allows consuming messages from Redis.
type ConsumerClient struct {
	baseManager
	GroupName               string
	MaxRetries              int
	DLQStreamName           string
	ProcessTimeout          time.Duration
	AbandonedMessageTimeout time.Duration
	TaskHandler             func(ctx context.Context, workerID string, data string) TaskResult
	ReadBatchSize           int64
}

// Ping checks the health of the Redis connection.
func (b *baseManager) Ping(ctx context.Context) error {
	if b.Client == nil {
		return fmt.Errorf("redis client is nil")
	}
	return b.Client.Ping(ctx).Err()
}

func getRedisOptions(redisConfig Config) *redis.Options {
	options := &redis.Options{Addr: redisConfig.Addr}
	redisPassword := os.Getenv("REDIS_PASSWORD")
	if redisPassword != "" {
		options.Password = redisPassword
	} else {
		logging.Warn(context.Background(), "Redis password environment variable is empty")
	}
	return options
}

// NewClient returns a new *redis.Client configured with standard options for the given Config.
func NewClient(redisConfig Config) *redis.Client {
	return redis.NewClient(getRedisOptions(redisConfig))
}
