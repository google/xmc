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
package redisclient

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"xmanager_cloud/logging/logging"
)

const (
	// DefaultMaxLen is the default max length for Redis streams. Set to 10000 to avoid Redis
	// streams growing indefinitely but still keep a decent history.
	DefaultMaxLen = 10000
)

// NewPublisher creates a new redis client for publishing messages.
func NewPublisher(redisConfig Config, streamName string) *PublisherClient {
	options := getRedisOptions(redisConfig)
	return &PublisherClient{
		baseManager: baseManager{
			Client:     redis.NewClient(options),
			StreamName: streamName,
		},
	}
}

// Close closes the Redis client.
func (p *PublisherClient) Close(ctx context.Context) error {
	return p.Client.Close()
}

// AddMessage adds a message to the Redis stream.
func (p *PublisherClient) AddMessage(ctx context.Context, data string, failedAttempts string) error {
	args := &redis.XAddArgs{
		Stream: p.StreamName,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]any{
			"data":           data,
			"failedAttempts": failedAttempts,
		},
	}
	if err := p.XAdd(ctx, args).Err(); err != nil {
		logging.Error(ctx, "Failed to add message to stream", "stream", p.StreamName, "error", err)
		return fmt.Errorf("failed to add message to stream %s: %w", p.StreamName, err)
	}
	return nil
}
