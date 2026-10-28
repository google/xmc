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

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/storage"
)

// pagedArtifactStorage serves ListArtifacts from fixed pages keyed by page token.
type pagedArtifactStorage struct {
	storage.MockStorage
	pages     map[string][]*essmsgpb.Artifact
	nextToken map[string]string
	listCalls int
}

func (p *pagedArtifactStorage) ListArtifacts(_ context.Context, _ string, _ int32, pageToken, _, _ string, _ time.Duration) ([]*essmsgpb.Artifact, string, error) {
	p.listCalls++
	if p.ListArtifactsErr != nil {
		return nil, "", p.ListArtifactsErr
	}
	return p.pages[pageToken], p.nextToken[pageToken], nil
}

func textArtifact() *essmsgpb.Artifact {
	return &essmsgpb.Artifact{Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{}}}}
}

func xprofArtifact() *essmsgpb.Artifact {
	return &essmsgpb.Artifact{Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: &essmsgpb.ManagedXprofArtifact{}}}}
}

// recordingXprofRule matches ManagedXprof artifacts and records superseded EIDs.
func recordingXprofRule(supersededEIDs *[]int64) artifactlifecycle.LifecycleRule {
	return artifactlifecycle.LifecycleRule{
		Name:                  "recording",
		Matches:               func(a *essmsgpb.Artifact) bool { return a.GetPayload().GetManagedXprof() != nil },
		TargetExecutionStates: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_RUNNING},
		OnExecutionSuperseded: func(_ context.Context, _ storage.Storage, _, _, oldEID int64, _ *essmsgpb.Artifact) error {
			*supersededEIDs = append(*supersededEIDs, oldEID)
			return nil
		},
	}
}

func TestRequiredNotifyWhen(t *testing.T) {
	var unused []int64
	tests := []struct {
		name          string
		registry      *artifactlifecycle.Registry
		listErr       error
		want          string
		wantListCalls int
	}{
		{name: "nil registry skips listing", registry: nil, want: "", wantListCalls: 0},
		{name: "empty registry skips listing", registry: artifactlifecycle.NewRegistry(), want: "", wantListCalls: 0},
		{
			name:          "matching artifact on second page is found",
			registry:      artifactlifecycle.NewRegistry(recordingXprofRule(&unused)),
			want:          "RUNNING",
			wantListCalls: 2,
		},
		{
			name:          "list error yields empty value",
			registry:      artifactlifecycle.NewRegistry(recordingXprofRule(&unused)),
			listErr:       errors.New("boom"),
			want:          "",
			wantListCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &pagedArtifactStorage{
				MockStorage: storage.MockStorage{ListArtifactsErr: tc.listErr},
				pages:       map[string][]*essmsgpb.Artifact{"": {textArtifact()}, "page-2": {xprofArtifact()}},
				nextToken:   map[string]string{"": "page-2"},
			}
			h := &baseHandler{storage: store, artifactLifecycle: tc.registry}

			if got := h.requiredNotifyWhen(context.Background(), 1, 2); got != tc.want {
				t.Errorf("requiredNotifyWhen() = %q, want %q", got, tc.want)
			}
			if store.listCalls != tc.wantListCalls {
				t.Errorf("ListArtifacts calls = %d, want %d", store.listCalls, tc.wantListCalls)
			}
		})
	}
}

func TestFinalizeSupersededExecution(t *testing.T) {
	tests := []struct {
		name             string
		oldExecutionName string
		want             []int64
	}{
		{name: "valid execution name finalizes old EID", oldExecutionName: "experiments/1/workUnits/2/executions/3", want: []int64{3}},
		{name: "invalid execution name is skipped", oldExecutionName: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []int64
			store := &storage.MockStorage{ListArtifactsResp: []*essmsgpb.Artifact{xprofArtifact()}}
			h := &WorkUnitHandler{baseHandler: baseHandler{
				storage:           store,
				artifactLifecycle: artifactlifecycle.NewRegistry(recordingXprofRule(&got)),
			}}

			h.finalizeSupersededExecution(context.Background(), 1, 2, tc.oldExecutionName)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("superseded EIDs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
