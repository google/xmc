// Copyright 2026 Google LLC
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

package jobset

import (
	"context"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
)

type mockRedisPublisher struct {
	messages []string
}

func (m *mockRedisPublisher) AddMessage(ctx context.Context, data string, failedAttempts string) error {
	m.messages = append(m.messages, data)
	return nil
}

func TestRedisLifecycleNotifier_NotifyLifecycleEvent(t *testing.T) {
	ctx := context.Background()
	pub := &mockRedisPublisher{}
	notifier := NewRedisLifecycleNotifier(pub)

	event := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(123),
		WorkUnitId:          proto.Int64(456),
		WorkUnitExecutionId: proto.Int64(1),
		OldState:            essmsgpb.WorkUnitStatus_STARTING.Enum(),
		NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
		Payload: &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{
			JobSetRuntimeContext: &taskqueuepb.JobSetRuntimeContext{
				JobSetName: proto.String("test-js"),
			},
		},
	}

	if err := notifier.NotifyLifecycleEvent(ctx, event); err != nil {
		t.Fatalf("NotifyLifecycleEvent failed: %v", err)
	}

	if len(pub.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(pub.messages))
	}

	unmarshaled := &taskqueuepb.ArtifactLifecycleEvent{}
	if err := protojson.Unmarshal([]byte(pub.messages[0]), unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal published message: %v", err)
	}

	if unmarshaled.GetExperimentId() != 123 || unmarshaled.GetWorkUnitId() != 456 {
		t.Errorf("unexpected event content: %v", unmarshaled)
	}
}
