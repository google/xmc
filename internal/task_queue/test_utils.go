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

	essmsgpb "github.com/google/xmc/api/protos/experiment"
	actuatortaskpb "github.com/google/xmc/api/protos/task_queue"
)

// EnqueuedTask represents a task enqueued for testing.
type EnqueuedTask struct {
	Action        *actuatortaskpb.Action
	Target        string
	WorkUnit      *essmsgpb.WorkUnit
	OperationName string
}

// FakeClient is a fake implementation of the taskqueue.Client interface for testing.
type FakeClient struct {
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

	// EnqueueErr is the error that will be returned by EnqueueWorkUnitTask.
	EnqueueErr error
	// EnqueueFunc is an optional function that will be called by EnqueueWorkUnitTask.
	EnqueueFunc func(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error
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
