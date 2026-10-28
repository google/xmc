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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/storage"
)

func TestRequiredNotifyWhenAnnotation(t *testing.T) {
	rule := artifactlifecycle.LifecycleRule{
		Name: "test-rule",
		Matches: func(a *essmsgpb.Artifact) bool {
			return a.GetPayload().GetManagedXprof() != nil
		},
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{
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

func TestRequiredNotifyWhenAnnotation_UnionOfMatchingRules(t *testing.T) {
	xprofRule := artifactlifecycle.LifecycleRule{
		Name:                  "xprof",
		Matches:               func(a *essmsgpb.Artifact) bool { return a.GetPayload().GetManagedXprof() != nil },
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_RUNNING},
	}
	textRule := artifactlifecycle.LifecycleRule{
		Name:                  "text",
		Matches:               func(a *essmsgpb.Artifact) bool { return a.GetPayload().GetText() != nil },
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_FAILED, essmsgpb.WorkUnitStatus_RUNNING},
	}
	unmatchedRule := artifactlifecycle.LifecycleRule{
		Name:                  "unmatched",
		Matches:               func(*essmsgpb.Artifact) bool { return false },
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_STOPPED},
	}
	reg := artifactlifecycle.NewRegistry(xprofRule, textRule, unmatchedRule)
	arts := []*essmsgpb.Artifact{
		{Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{}}}},
		{Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: &essmsgpb.ManagedXprofArtifact{}}}},
	}

	const want = "RUNNING.FAILED"
	if got := reg.RequiredNotifyWhenAnnotation(arts); got != want {
		t.Errorf("RequiredNotifyWhenAnnotation() = %q, want %q", got, want)
	}
}

func TestRegistryDispatch(t *testing.T) {
	ctx := context.Background()
	var stateChangedCalls, supersededCalls int
	rule := artifactlifecycle.LifecycleRule{
		Name: "test-rule",
		Matches: func(a *essmsgpb.Artifact) bool {
			return a.GetPayload().GetManagedXprof() != nil
		},
		OnExecutionStateChanged: func(_ context.Context, _ storage.Storage, _ *taskqueuepb.ArtifactLifecycleEvent, _ *essmsgpb.Artifact) error {
			stateChangedCalls++
			return nil
		},
		OnExecutionSuperseded: func(_ context.Context, _ storage.Storage, _, _, _ int64, _ *essmsgpb.Artifact) error {
			supersededCalls++
			return nil
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
	if stateChangedCalls != 1 {
		t.Errorf("stateChangedCalls = %d, want 1", stateChangedCalls)
	}

	if err := reg.HandleExecutionSuperseded(ctx, mockStore, 1, 2, 3); err != nil {
		t.Fatalf("HandleExecutionSuperseded failed: %v", err)
	}
	if supersededCalls != 1 {
		t.Errorf("supersededCalls = %d, want 1", supersededCalls)
	}
}

// pagedArtifactStorage serves ListArtifacts from fixed pages keyed by page token.
type pagedArtifactStorage struct {
	storage.MockStorage
	pages     map[string][]*essmsgpb.Artifact
	nextToken map[string]string
	gotTokens []string
}

func (p *pagedArtifactStorage) ListArtifacts(_ context.Context, _ string, _ int32, pageToken, _, _ string, _ time.Duration) ([]*essmsgpb.Artifact, string, error) {
	p.gotTokens = append(p.gotTokens, pageToken)
	return p.pages[pageToken], p.nextToken[pageToken], nil
}

func TestListWorkUnitArtifacts_FollowsPageTokens(t *testing.T) {
	first := &essmsgpb.Artifact{Name: "artifacts/1"}
	second := &essmsgpb.Artifact{Name: "artifacts/2"}
	store := &pagedArtifactStorage{
		pages:     map[string][]*essmsgpb.Artifact{"": {first}, "page-2": {second}},
		nextToken: map[string]string{"": "page-2"},
	}

	got, err := artifactlifecycle.ListWorkUnitArtifacts(context.Background(), store, 1, 2)
	if err != nil {
		t.Fatalf("ListWorkUnitArtifacts() error = %v", err)
	}
	if diff := cmp.Diff([]*essmsgpb.Artifact{first, second}, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListWorkUnitArtifacts() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"", "page-2"}, store.gotTokens); diff != "" {
		t.Errorf("page tokens mismatch (-want +got):\n%s", diff)
	}
}

func TestListWorkUnitArtifacts_PropagatesError(t *testing.T) {
	store := &storage.MockStorage{ListArtifactsErr: errors.New("boom")}
	if _, err := artifactlifecycle.ListWorkUnitArtifacts(context.Background(), store, 1, 2); err == nil {
		t.Error("ListWorkUnitArtifacts() error = nil, want error")
	}
}
