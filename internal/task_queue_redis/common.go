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

	"github.com/redis/go-redis/v9"
	"go/log"
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
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
	GetQueueDepth(ctx context.Context) (int64, error)
}

// Consumer is an interface for consuming messages from Redis.
type Consumer interface {
	InitializeClient(ctx context.Context)
	StartWorker(ctx context.Context, workerID string)
	RunCleanup(ctx context.Context, workerID string)
	SetTaskHandler(taskHandler func(ctx context.Context, workerID string, data string) TaskResult)
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

type baseManager struct {
	*redis.Client
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
		log.Warningf("Redis password environment variable is empty")
	}
	return options
}
