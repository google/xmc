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

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultMaxLen is the approximate maximum length for the broadcast Redis stream.
	// Approximate trimming (`~ 100`) is used to minimize Redis memory and CPU overhead.
	DefaultMaxLen = 100
)

var _ Publisher = (*PublisherClient)(nil)

type redisPublisherClient interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	Close() error
}

// PublisherClient is a concrete Redis stream publisher implementing Publisher.
type PublisherClient struct {
	client     redisPublisherClient
	streamName string
}

// NewPublisher creates a new PublisherClient using an existing redis.Client.
func NewPublisher(client *redis.Client, streamName string) *PublisherClient {
	var rc redisPublisherClient
	if client != nil {
		rc = client
	}
	return &PublisherClient{
		client:     rc,
		streamName: streamName,
	}
}

// Publish broadcasts a message string to the Redis stream using XADD with approximate trimming.
func (p *PublisherClient) Publish(ctx context.Context, message string) error {
	if p.client == nil {
		return errors.New("redis client is not initialized")
	}
	if p.streamName == "" {
		return errors.New("stream name is empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	args := &redis.XAddArgs{
		Stream: p.streamName,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]any{
			"message": message,
		},
	}
	if err := p.client.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("failed to publish broadcast message to stream %q: %w", p.streamName, err)
	}
	return nil
}

// Close closes the underlying Redis client connection.
func (p *PublisherClient) Close() error {
	if p.client == nil {
		return nil
	}
	return p.client.Close()
}
