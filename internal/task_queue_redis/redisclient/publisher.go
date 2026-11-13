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
	if err := p.XAdd(ctx, xAddArgs(p.StreamName, data, failedAttempts)).Err(); err != nil {
		return fmt.Errorf("failed to add message to stream %s: %w", p.StreamName, err)
	}
	return nil
}

// AddMessages adds new messages (no failed attempts) to the Redis stream in a single pipelined
// round trip. A non-nil error does not guarantee the message was not added: the reply may be lost
// after Redis applied the XADD, and go-redis may retry the pipeline, so consumers must tolerate
// duplicates.
func (p *PublisherClient) AddMessages(ctx context.Context, data []string) []error {
	return addMessages(ctx, p.Client, p.StreamName, data)
}

// streamPipeliner runs queued Redis commands in one pipelined round trip. *redis.Client satisfies it.
type streamPipeliner interface {
	Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error)
}

// addMessages XADDs each of data to stream through one pipeline and returns one error per message,
// nil for the messages that were added. go-redis sets the error of every queued command when the
// round trip fails, and returns the first command error from Pipelined, so the per-command errors
// are authoritative.
func addMessages(ctx context.Context, client streamPipeliner, stream string, data []string) []error {
	errs := make([]error, len(data))
	if len(data) == 0 {
		return errs
	}
	cmds := make([]*redis.StringCmd, len(data))
	// The pipeline error is the first command error, reported per command below.
	_, _ = client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, d := range data {
			cmds[i] = pipe.XAdd(ctx, xAddArgs(stream, d, "0"))
		}
		return nil
	})
	for i, cmd := range cmds {
		if err := cmd.Err(); err != nil {
			errs[i] = fmt.Errorf("failed to add message to stream %s: %w", stream, err)
		}
	}
	return errs
}

func xAddArgs(stream, data, failedAttempts string) *redis.XAddArgs {
	return &redis.XAddArgs{
		Stream: stream,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]any{
			"data":           data,
			"failedAttempts": failedAttempts,
		},
	}
}
