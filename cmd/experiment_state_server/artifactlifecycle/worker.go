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

package artifactlifecycle

import (
	"context"

	"google.golang.org/protobuf/encoding/protojson"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
)

// LifecycleRegistry abstracts the lifecycle registry operations for the worker.
type LifecycleRegistry interface {
	HandleExecutionStateChanged(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent) error
}

// Worker handles artifact lifecycle stream tasks dispatched from Redis.
type Worker struct {
	storage  storage.Storage
	registry LifecycleRegistry
}

// NewWorker creates a new Worker.
func NewWorker(storage storage.Storage, registry LifecycleRegistry) *Worker {
	return &Worker{
		storage:  storage,
		registry: registry,
	}
}

// HandleTask processes an individual Redis stream message containing an ArtifactLifecycleEvent.
func (w *Worker) HandleTask(ctx context.Context, workerID string, data string) redisclient.TaskResult {
	event := &taskqueuepb.ArtifactLifecycleEvent{}
	if err := protojson.Unmarshal([]byte(data), event); err != nil {
		logging.Error(ctx, "Failed to unmarshal ArtifactLifecycleEvent", "data", data, "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: err}
	}

	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(event.GetExperimentId()))
	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(event.GetExperimentId(), event.GetWorkUnitId(), event.GetWorkUnitExecutionId()))
	logging.Info(ctx, "Processing artifact lifecycle event", "oldState", event.GetOldState().String(), "newState", event.GetNewState().String())

	if err := w.registry.HandleExecutionStateChanged(ctx, w.storage, event); err != nil {
		logging.Error(ctx, "Failed to handle artifact lifecycle state change", "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: err}
	}

	logging.Info(ctx, "Successfully processed artifact lifecycle event")
	return redisclient.TaskResult{WasRunSuccessful: true}
}
