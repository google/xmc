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
package client

import (
	"context"
	"testing"

	cloudtaskspb "cloud/tasks/v2/cloudtasks_go_proto"
	taskpb "cloud/tasks/v2/task_go_proto"
	"golang/github_com/gax/v/v2/v2/gax"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/security/securitytest"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

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

	// --- Method Configurations ---
	// EnqueueErr is the error that will be returned by EnqueueWorkUnitTask.
	EnqueueErr error
}

// EnqueueWorkUnitTask captures the input and returns the configured error.
func (c *FakeClient) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	if c.EnqueueErr != nil {
		return c.EnqueueErr
	}
	c.LastEnqueuedAction = action
	c.LastEnqueuedTarget = target
	c.LastEnqueuedWorkUnit = workUnit
	c.LastEnqueuedOperationName = operationName
	return nil
}

// Close is a no-op for the mock client.
func (c *FakeClient) Close() error {
	return nil
}

// MockGCPTasksClient is a mock implementation of our gcpTasksClient interface.
type MockGCPTasksClient struct {
	// CreateTaskFunc allows tests to substitute the behavior of CreateTask.
	CreateTaskFunc func(ctx context.Context, req *cloudtaskspb.CreateTaskRequest) (*taskpb.Task, error)
	// CloseFunc allows tests to substitute the behavior of Close.
	CloseFunc func() error

	// ReceivedRequests stores the requests made to CreateTask for later inspection.
	ReceivedRequests []*cloudtaskspb.CreateTaskRequest
}

// CreateTask is the mock's implementation of the interface method.
func (m *MockGCPTasksClient) CreateTask(ctx context.Context, req *cloudtaskspb.CreateTaskRequest, opts ...gax.CallOption) (*taskpb.Task, error) {
	// Store the request so we can assert on it.
	m.ReceivedRequests = append(m.ReceivedRequests, req)

	if m.CreateTaskFunc != nil {
		return m.CreateTaskFunc(ctx, req)
	}
	// Default behavior: return a successful response.
	return &taskpb.Task{Name: "projects/p/locations/l/queues/q/tasks/mock-task"}, nil
}

// Close is the mock's implementation of the interface method.
func (m *MockGCPTasksClient) Close() error {
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	// Default behavior: do nothing and return no error.
	return nil
}

// CreateTaskQueueClient creates a task queue client for testing.
func CreateTaskQueueClient(ctx context.Context, t *testing.T, gcpTasksClient *MockGCPTasksClient) *Client {
	t.Helper()
	keyFunc := securitytest.MustCreateTestSigner(t)
	taskQueueClient, err := New(ctx, gcpTasksClient, "test-project", "test-location", "test-queue", "test-sa", "http://actuator.example.com", "test-audience", keyFunc, DefaultSignatureTTLSeconds, false, "")
	if err != nil {
		t.Fatalf("Failed to create task queue client: %v", err)
	}
	return taskQueueClient
}
