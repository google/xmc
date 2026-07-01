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
package client

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/security/securitytest"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/task_queue_redis/redisclient"
)

// mockAndConfigureRedisClient configures a fake Redis client for the Task Queue client.
func mockAndConfigureRedisClient(t *testing.T, taskQueueClient *Client) *redisclient.FakeClient {
	t.Helper()
	fakeClient := redisclient.NewFakeClient()
	taskQueueClient.redisClient = fakeClient
	return fakeClient
}

// GetFakeRedisClient returns the underlying fake Redis client from a task queue client.
func GetFakeRedisClient(c *Client) *redisclient.FakeClient {
	if fake, ok := c.redisClient.(*redisclient.FakeClient); ok {
		return fake
	}
	return nil
}

// MockGenerateOIDCTokenForTest mocks the OIDC token generation for testing.
func MockGenerateOIDCTokenForTest() func() {
	original := generateOIDCToken
	generateOIDCToken = func(ctx context.Context, serviceAccountEmail string, audience string) (string, error) {
		return "mock-oidc-token", nil
	}
	return func() {
		generateOIDCToken = original
	}
}

// EnqueuedTask represents a task enqueued for testing.
type EnqueuedTask struct {
	Action        *actuatortaskpb.Action
	Target        string
	WorkUnit      *essmsgpb.WorkUnit
	OperationName string
}

// FakeClient is a fake implementation of the taskqueue.Client interface for testing.
type FakeClient struct {
	// --- Captured Inputs ---
	// LastEnqueuedAction holds the last action that was passed to EnqueueWorkUnitTask.
	LastEnqueuedAction *actuatortaskpb.Action
	// LastEnqueuedTarget holds the last target that was passed to EnqueueWorkUnitTask.
	LastEnqueuedTarget string
	// LastEnqueuedWorkUnit holds the last work unit that was passed to EnqueueWorkUnitTask.
	LastEnqueuedWorkUnit *essmsgpb.WorkUnit
	// LastEnqueuedOperationName holds the last operation name that was passed to EnqueueWorkUnitTask.
	LastEnqueuedOperationName string

	// EnqueuedTasks holds all enqueued tasks.
	EnqueuedTasks []EnqueuedTask

	// --- Method Configurations ---
	// EnqueueErr is the error that will be returned by EnqueueWorkUnitTask.
	EnqueueErr error
	// EnqueueFunc is an optional function that will be called by EnqueueWorkUnitTask.
	// If set, it will be called instead of returning EnqueueErr.
	EnqueueFunc func(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error

	// GetQueueDepthErr is the error that will be returned by GetQueueDepth.
	GetQueueDepthErr error
	// MockQueueDepth is the mocked value returned by GetQueueDepth.
	MockQueueDepth int64
}

// EnqueueWorkUnitTask captures the input and returns the configured error.
func (c *FakeClient) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	c.LastEnqueuedAction = action
	c.LastEnqueuedTarget = target
	c.LastEnqueuedWorkUnit = workUnit
	c.LastEnqueuedOperationName = operationName
	c.EnqueuedTasks = append(c.EnqueuedTasks, EnqueuedTask{
		Action:        action,
		Target:        target,
		WorkUnit:      workUnit,
		OperationName: operationName,
	})
	if c.EnqueueFunc != nil {
		return c.EnqueueFunc(ctx, target, workUnit, action, operationName)
	}
	if c.EnqueueErr != nil {
		return c.EnqueueErr
	}
	return nil
}

// Close is a no-op for the mock client.
func (c *FakeClient) Close() error {
	return nil
}

// GetQueueDepth returns the mocked queue depth or the length of EnqueuedTasks.
func (c *FakeClient) GetQueueDepth(ctx context.Context) (int64, error) {
	if c.GetQueueDepthErr != nil {
		return 0, c.GetQueueDepthErr
	}
	if c.MockQueueDepth > 0 {
		return c.MockQueueDepth, nil
	}
	return int64(len(c.EnqueuedTasks)), nil
}

// CreateTaskQueueClient creates a task queue client for testing.
func CreateTaskQueueClient(ctx context.Context, t *testing.T) *Client {
	t.Helper()
	keyFunc := securitytest.MustCreateTestSigner(t)
	taskQueueClient, err := New(ctx, "test-project", "test-sa", "http://actuator.example.com", "test-audience", keyFunc, DefaultSignatureTTLSeconds, "")
	if err != nil {
		t.Fatalf("Failed to create task queue client: %v", err)
	}
	mockAndConfigureRedisClient(t, taskQueueClient)
	return taskQueueClient
}
