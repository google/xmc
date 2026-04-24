// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package redisclient

import (
	"context"
	"sync"
)

// FakeClient is a fake implementation of Redis client for testing. It fits both Publisher and Consumer interfaces.
type FakeClient struct {
	mu          sync.Mutex
	Messages    []string
	taskHandler func(context.Context, string, string) TaskResult
	PingError   error
}

// NewFakeClient creates a new FakeClient.
func NewFakeClient() *FakeClient {
	return &FakeClient{
		Messages: []string{},
	}
}

// AddMessage adds a message to the fake client.
func (c *FakeClient) AddMessage(ctx context.Context, data string, failedAttempts string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Messages = append(c.Messages, data)
}

// GetStreamLen returns the number of messages added.
func (c *FakeClient) GetStreamLen(ctx context.Context, stream string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.Messages)), nil
}

// InitializeClient is a fake implementation of RedisClient.InitializeClient.
func (c *FakeClient) InitializeClient(ctx context.Context) {}

// Reset is a fake implementation of RedisClient.Reset.
func (c *FakeClient) Reset(ctx context.Context) {}

// DeleteStream is a fake implementation of RedisClient.DeleteStream.
func (c *FakeClient) DeleteStream(ctx context.Context, streamName string) {}

// AckMessage is a fake implementation of RedisClient.AckMessage.
func (c *FakeClient) AckMessage(ctx context.Context, msgID string) {}

// StartWorker is a fake implementation of RedisClient.StartWorker.
func (c *FakeClient) StartWorker(ctx context.Context, workerID string) {}

// RunCleanup is a fake implementation of RedisClient.RunCleanup.
func (c *FakeClient) RunCleanup(ctx context.Context, workerID string) {}

// GetPendingCount is a fake implementation of RedisClient.GetPendingCount.
func (c *FakeClient) GetPendingCount(ctx context.Context) (int, error) {
	return 0, nil
}

// SetTaskHandler sets the task handler function for the fake client.
func (c *FakeClient) SetTaskHandler(taskHandler func(context.Context, string, string) TaskResult) {
	c.taskHandler = taskHandler
}

// Ping is a fake implementation of RedisClient.Ping.
func (c *FakeClient) Ping(ctx context.Context) error {
	return c.PingError
}
