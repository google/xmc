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

package artifactlifecycle_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/storage"
)

type fakeRule struct {
	name              string
	matchesFn         func(a *essmsgpb.Artifact) bool
	targetStates      []essmsgpb.WorkUnitStatus_WorkUnitState
	stateChangedCalls int
	supersededCalls   int
}

func (f *fakeRule) Name() string { return f.name }
func (f *fakeRule) Matches(a *essmsgpb.Artifact) bool {
	if f.matchesFn != nil {
		return f.matchesFn(a)
	}
	return false
}
func (f *fakeRule) TargetExecutionStates() []essmsgpb.WorkUnitStatus_WorkUnitState {
	return f.targetStates
}
func (f *fakeRule) OnExecutionStateChanged(_ context.Context, _ storage.Storage, _ *taskqueuepb.ArtifactLifecycleEvent, _ *essmsgpb.Artifact) error {
	f.stateChangedCalls++
	return nil
}
func (f *fakeRule) OnExecutionSuperseded(_ context.Context, _ storage.Storage, _, _, _ int64, _ *essmsgpb.Artifact) error {
	f.supersededCalls++
	return nil
}

func TestRequiredNotifyWhenAnnotation(t *testing.T) {
	rule := &fakeRule{
		name: "test-rule",
		matchesFn: func(a *essmsgpb.Artifact) bool {
			return a.GetPayload().GetManagedXprof() != nil
		},
		targetStates: []essmsgpb.WorkUnitStatus_WorkUnitState{
			essmsgpb.WorkUnitStatus_STARTING,
			essmsgpb.WorkUnitStatus_RUNNING,
			essmsgpb.WorkUnitStatus_COMPLETED,
		},
	}
	reg := artifactlifecycle.NewRegistry(rule)

	t.Run("empty artifacts returns empty string", func(t *testing.T) {
		if got := reg.RequiredNotifyWhenAnnotation(nil); got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("non matching artifact returns empty string", func(t *testing.T) {
		art := &essmsgpb.Artifact{
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_Text{
					Text: &essmsgpb.TextArtifact{Text: "notes"},
				},
			},
		}
		if got := reg.RequiredNotifyWhenAnnotation([]*essmsgpb.Artifact{art}); got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("matching artifact returns target states joined with dot", func(t *testing.T) {
		art := &essmsgpb.Artifact{
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
				},
			},
		}
		want := "STARTING.RUNNING.COMPLETED"
		if got := reg.RequiredNotifyWhenAnnotation([]*essmsgpb.Artifact{art}); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestRegistryDispatch(t *testing.T) {
	ctx := context.Background()
	rule := &fakeRule{
		name: "test-rule",
		matchesFn: func(a *essmsgpb.Artifact) bool {
			return a.GetPayload().GetManagedXprof() != nil
		},
	}
	reg := artifactlifecycle.NewRegistry(rule)

	mockStore := &storage.MockStorage{
		ListArtifactsResp: []*essmsgpb.Artifact{
			{
				Name: "experiments/1/workUnits/2/artifacts/3",
				Payload: &essmsgpb.ArtifactPayload{
					Type: &essmsgpb.ArtifactPayload_ManagedXprof{
						ManagedXprof: &essmsgpb.ManagedXprofArtifact{},
					},
				},
			},
		},
	}

	event := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(1),
		WorkUnitId:          proto.Int64(2),
		WorkUnitExecutionId: proto.Int64(3),
		NewState:            essmsgpb.WorkUnitStatus_RUNNING.Enum(),
	}

	if err := reg.HandleExecutionStateChanged(ctx, mockStore, event); err != nil {
		t.Fatalf("HandleExecutionStateChanged failed: %v", err)
	}
	if rule.stateChangedCalls != 1 {
		t.Errorf("stateChangedCalls = %d, want 1", rule.stateChangedCalls)
	}

	if err := reg.HandleExecutionSuperseded(ctx, mockStore, 1, 2, 3); err != nil {
		t.Fatalf("HandleExecutionSuperseded failed: %v", err)
	}
	if rule.supersededCalls != 1 {
		t.Errorf("supersededCalls = %d, want 1", rule.supersededCalls)
	}
}
