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
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/storage"
)

type mockLifecycleRegistry struct {
	handledEvents []*taskqueuepb.ArtifactLifecycleEvent
	handleErr     error
}

func (m *mockLifecycleRegistry) HandleExecutionStateChanged(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent) error {
	m.handledEvents = append(m.handledEvents, event)
	return m.handleErr
}

func TestWorker_HandleTask_Success(t *testing.T) {
	ctx := context.Background()
	reg := &mockLifecycleRegistry{}
	worker := NewWorker(nil, reg)

	event := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(100),
		WorkUnitId:          proto.Int64(1),
		WorkUnitExecutionId: proto.Int64(2),
		OldState:            essmsgpb.WorkUnitStatus_STARTING.Enum(),
		NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
	}
	data, err := protojson.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	result := worker.HandleTask(ctx, "test-worker", string(data))
	if !result.WasRunSuccessful {
		t.Fatalf("expected successful run, got error: %v", result.Error)
	}
	if len(reg.handledEvents) != 1 {
		t.Fatalf("expected 1 handled event, got %d", len(reg.handledEvents))
	}
	if reg.handledEvents[0].GetExperimentId() != 100 || reg.handledEvents[0].GetWorkUnitId() != 1 {
		t.Errorf("unexpected event: %v", reg.handledEvents[0])
	}
}

func TestWorker_HandleTask_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	reg := &mockLifecycleRegistry{}
	worker := NewWorker(nil, reg)

	result := worker.HandleTask(ctx, "test-worker", "{not valid json")
	if result.WasRunSuccessful {
		t.Fatalf("expected failure on invalid json")
	}
	if result.ShouldBeRetried {
		t.Errorf("expected non-retryable on invalid json")
	}
}

func TestWorker_HandleTask_HandleError(t *testing.T) {
	ctx := context.Background()
	reg := &mockLifecycleRegistry{handleErr: errors.New("transient error")}
	worker := NewWorker(nil, reg)

	event := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(100),
		WorkUnitId:          proto.Int64(1),
		WorkUnitExecutionId: proto.Int64(2),
		NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
	}
	data, err := protojson.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	result := worker.HandleTask(ctx, "test-worker", string(data))
	if result.WasRunSuccessful {
		t.Fatalf("expected failure on registry error")
	}
	if !result.ShouldBeRetried {
		t.Errorf("expected retryable on registry error")
	}
}
