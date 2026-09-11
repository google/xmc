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
	"sync"
)

var (
	_ Publisher = (*PublisherClient)(nil)
	_ Consumer  = (*ConsumerClient)(nil)
)

// Config holds the Redis connection configuration for the broadcast service.
type Config struct {
	Addr string
}

// Publisher defines the contract for broadcasting state update notifications.
type Publisher interface {
	Publish(ctx context.Context, message string) error
}

// Consumer defines the contract for subscribing to broadcast notifications.
type Consumer interface {
	Subscribe(ctx context.Context, startID string, handler func(msg string))
}

// PublisherClient handles publishing messages to a broadcast stream.
// Currently implemented as a stub double.
// TODO: Replace stub implementation with real Redis XADD capped to MAXLEN ~ 100.
type PublisherClient struct {
	streamName string
	mu         sync.RWMutex
	messages   []string
}

// NewPublisher creates a new PublisherClient stub.
// TODO: Connect to Redis using redis.NewClient.
func NewPublisher(redisConfig Config, streamName string) *PublisherClient {
	return &PublisherClient{
		streamName: streamName,
		messages:   make([]string, 0),
	}
}

// Publish publishes a message to the broadcast stream.
// TODO: Replace stub with real Redis XADD call with MAXLEN ~ 100.
func (p *PublisherClient) Publish(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, message)
	return nil
}

// Messages returns a copy of all published messages for test assertions.
func (p *PublisherClient) Messages() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]string, len(p.messages))
	copy(res, p.messages)
	return res
}

// ConsumerClient handles consuming messages from a broadcast stream.
// Currently implemented as a stub double.
// TODO: Replace stub implementation with real Redis blocking XREAD loop.
type ConsumerClient struct {
	streamName string
}

// NewConsumer creates a new ConsumerClient stub.
// TODO: Connect to Redis using redis.NewClient.
func NewConsumer(redisConfig Config, streamName string) *ConsumerClient {
	return &ConsumerClient{
		streamName: streamName,
	}
}

// Subscribe consumes messages starting from startID and invokes handler for each message.
// TODO: Replace stub with real Redis blocking XREAD loop starting from startID.
func (c *ConsumerClient) Subscribe(ctx context.Context, startID string, handler func(msg string)) {
	if handler == nil {
		return
	}
	<-ctx.Done()
}
