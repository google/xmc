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
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultBlockTimeout is the duration XREAD will block waiting for new broadcast messages.
	DefaultBlockTimeout = 2 * time.Second
)

var _ Consumer = (*ConsumerClient)(nil)

type redisConsumerClient interface {
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
	Close() error
}

// ConsumerClient is a concrete Redis stream consumer implementing Consumer.
type ConsumerClient struct {
	client       redisConsumerClient
	streamName   string
	blockTimeout time.Duration
}

// NewConsumer creates a new ConsumerClient using an existing redis.Client.
func NewConsumer(client *redis.Client, streamName string) *ConsumerClient {
	var rc redisConsumerClient
	if client != nil {
		rc = client
	}
	return &ConsumerClient{
		client:       rc,
		streamName:   streamName,
		blockTimeout: DefaultBlockTimeout,
	}
}

// Close closes the underlying Redis client connection.
func (c *ConsumerClient) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

// LatestOffset queries the stream using XREVRANGE to locate the ID of the latest message.
// If the stream is empty or does not exist, it returns "0", nil.
func (c *ConsumerClient) LatestOffset(ctx context.Context) (string, error) {
	if c.client == nil {
		return "", errors.New("redis client is not initialized")
	}
	if c.streamName == "" {
		return "", errors.New("stream name is empty")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	res, err := c.client.XRevRangeN(ctx, c.streamName, "+", "-", 1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "0", nil
		}
		return "", fmt.Errorf("failed to query latest offset for stream %q: %w", c.streamName, err)
	}
	if len(res) == 0 {
		return "0", nil
	}
	return res[0].ID, nil
}

// Subscribe blocks listening for broadcast notifications starting from startID.
// It returns nil when the context is canceled (graceful shutdown), or a
// non-nil error if an unexpected connection or operational failure occurs.
func (c *ConsumerClient) Subscribe(ctx context.Context, startID string, handler func(msg string)) error {
	if handler == nil {
		return nil
	}
	if c.client == nil {
		return errors.New("redis client is not initialized")
	}
	if c.streamName == "" {
		return errors.New("stream name is empty")
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	currentID := startID
	if currentID == "" {
		currentID = "$"
	}

	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		entries, err := c.readMessages(ctx, currentID)
		if err != nil {
			retry, handleErr := c.handleReadError(ctx, err)
			if retry {
				continue
			}
			return handleErr
		}

		currentID = c.dispatchMessages(entries, currentID, handler)
	}
}

func (c *ConsumerClient) handleReadError(ctx context.Context, err error) (bool, error) {
	if errors.Is(err, redis.Nil) {
		return true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return false, nil
		}
		return false, ctxErr
	}
	return false, fmt.Errorf("failed to read from broadcast stream %q: %w", c.streamName, err)
}

func (c *ConsumerClient) readMessages(ctx context.Context, currentID string) ([]redis.XStream, error) {
	return c.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{c.streamName, currentID},
		Block:   c.blockTimeout,
	}).Result()
}

func (c *ConsumerClient) dispatchMessages(entries []redis.XStream, fallbackID string, handler func(msg string)) string {
	latestID := fallbackID
	for _, stream := range entries {
		for _, xmsg := range stream.Messages {
			latestID = xmsg.ID
			if rawMsg, ok := xmsg.Values["message"]; ok {
				if strMsg, ok := rawMsg.(string); ok {
					handler(strMsg)
				} else if byteMsg, ok := rawMsg.([]byte); ok {
					handler(string(byteMsg))
				}
			}
		}
	}
	return latestID
}
