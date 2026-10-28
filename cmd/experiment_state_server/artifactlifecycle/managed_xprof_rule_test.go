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
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/cmd/experiment_state_server/artifactlifecycle"
	"xmanager_cloud/internal/hypercomputeclient"
	"xmanager_cloud/internal/storage"
)

const (
	testProject  = "test-proj"
	testLocation = "us-central1"
	artifactName = "experiments/10/workUnits/20/artifacts/30"
)

// fakeMLRunClient returns MLRun resources without a "url" field, so the rule falls
// back to the deterministic console URL.
type fakeMLRunClient struct {
	createdRuns map[string]string
	updatedRuns map[string]string
}

func newFakeMLRunClient() *fakeMLRunClient {
	return &fakeMLRunClient{
		createdRuns: make(map[string]string),
		updatedRuns: make(map[string]string),
	}
}

func (f *fakeMLRunClient) CreateMLRun(_ context.Context, name, _, runPhase string, _ *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	f.createdRuns[name] = runPhase
	return map[string]any{"name": name}, nil
}

func (f *fakeMLRunClient) GetMLRun(_ context.Context, name string) (map[string]any, error) {
	return map[string]any{"name": name, "labels": map[string]any{"created_by": "xmanager"}}, nil
}

func (f *fakeMLRunClient) UpdateMLRun(_ context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	if opts.RunPhase != "" {
		f.updatedRuns[name] = opts.RunPhase
	}
	return map[string]any{"name": name}, nil
}

func managedXprofArtifact(xprofURL string) *essmsgpb.Artifact {
	mx := &essmsgpb.ManagedXprofArtifact{}
	if xprofURL != "" {
		mx.Xprof = &essmsgpb.XProfUIInstance{XprofUrl: xprofURL}
	}
	return &essmsgpb.Artifact{
		Name:    artifactName,
		Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: mx}},
	}
}

func storeWithCurrentEID(eid string) *storage.MockStorage {
	return &storage.MockStorage{
		WorkUnit: &essmsgpb.WorkUnit{
			Name:                     "experiments/10/workUnits/20",
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "experiments/10/workUnits/20/executions/" + eid},
		},
		Artifact: &essmsgpb.Artifact{
			Name: artifactName,
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "persisted-display-name",
						RunGroup:    "persisted-run-group",
					},
				},
			},
		},
	}
}

func runtimeContext() *taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext {
	return &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{
		JobSetRuntimeContext: &taskqueuepb.JobSetRuntimeContext{
			Cluster:    proto.String("cluster-1"),
			Namespace:  proto.String("default"),
			JobSetName: proto.String("xmc-wu-20-1"),
		},
	}
}

func lifecycleEvent(state essmsgpb.WorkUnitStatus_WorkUnitState, withRuntime bool) *taskqueuepb.ArtifactLifecycleEvent {
	event := &taskqueuepb.ArtifactLifecycleEvent{
		ExperimentId:        proto.Int64(10),
		WorkUnitId:          proto.Int64(20),
		WorkUnitExecutionId: proto.Int64(1),
		NewState:            state.Enum(),
	}
	if withRuntime {
		event.Payload = runtimeContext()
	}
	return event
}

// expectedMLRunID is the id the reconciler derives for runtimeContext().
var expectedMLRunID = hypercomputeclient.ConstructMLRunID("cluster-1", "default", "JobSet", "xmc-wu-20-1", time.Time{})

func TestManagedXprofRule_Matches(t *testing.T) {
	rule := artifactlifecycle.NewManagedXprofRule(nil, testProject, testLocation)
	if !rule.Matches(managedXprofArtifact("")) {
		t.Errorf("Matches() = false for ManagedXprof artifact")
	}
	text := &essmsgpb.Artifact{Payload: &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{}}}}
	if rule.Matches(text) {
		t.Errorf("Matches() = true for Text artifact")
	}
}

func TestManagedXprofRule_ActiveState(t *testing.T) {
	wantURL := hypercomputeclient.ConsoleURL(testLocation, expectedMLRunID, testProject)
	tests := []struct {
		name        string
		currentEID  string
		withRuntime bool
		wantCreated map[string]string
		wantURL     string // "" means no artifact update expected.
	}{
		{
			name:        "creates MLRun and persists console URL on matching EID",
			currentEID:  "1",
			withRuntime: true,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
			wantURL:     wantURL,
		},
		{
			name:        "skips persisting URL when execution is superseded",
			currentEID:  "2",
			withRuntime: true,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
		},
		{
			name:        "skips MLRun creation without runtime context",
			currentEID:  "1",
			withRuntime: false,
			wantCreated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)
			store := storeWithCurrentEID(tc.currentEID)

			if err := rule.OnExecutionStateChanged(context.Background(), store, lifecycleEvent(essmsgpb.WorkUnitStatus_RUNNING, tc.withRuntime), managedXprofArtifact("")); err != nil {
				t.Fatalf("OnExecutionStateChanged() error = %v", err)
			}
			if diff := cmp.Diff(tc.wantCreated, mlClient.createdRuns); diff != "" {
				t.Errorf("created runs mismatch (-want +got):\n%s", diff)
			}
			assertArtifactUpdate(t, store, tc.wantURL)
		})
	}
}

func assertArtifactUpdate(t *testing.T, store *storage.MockStorage, wantURL string) {
	t.Helper()
	update := store.LastArtifactUpdate
	if wantURL == "" {
		if update != nil {
			t.Errorf("unexpected artifact update: %v", update)
		}
		return
	}
	if update == nil {
		t.Fatal("expected an artifact update, got none")
	}
	if diff := cmp.Diff([]string{"url", "payload"}, update.UpdateMask.GetPaths()); diff != "" {
		t.Errorf("update mask mismatch (-want +got):\n%s", diff)
	}
	mx := update.Artifact.GetPayload().GetManagedXprof()
	if got := mx.GetXprof().GetXprofUrl(); got != wantURL {
		t.Errorf("xprof_url = %q, want %q", got, wantURL)
	}
	if got := update.Artifact.GetUrl().GetUrl(); got != wantURL {
		t.Errorf("url = %q, want %q", got, wantURL)
	}
	if mx.GetDisplayName() != "persisted-display-name" || mx.GetRunGroup() != "persisted-run-group" {
		t.Errorf("display name/run group not preserved: %v", mx)
	}
}

func TestManagedXprofRule_TerminalState(t *testing.T) {
	storedURL := hypercomputeclient.ConsoleURL(testLocation, "stored-run-1", testProject)
	tests := []struct {
		name        string
		state       essmsgpb.WorkUnitStatus_WorkUnitState
		withRuntime bool
		storedURL   string
		wantUpdated map[string]string
	}{
		{
			name: "COMPLETED finalizes derived MLRun", state: essmsgpb.WorkUnitStatus_COMPLETED, withRuntime: true,
			wantUpdated: map[string]string{expectedMLRunID: "COMPLETED"},
		},
		{
			name: "STOPPED finalizes derived MLRun as COMPLETED", state: essmsgpb.WorkUnitStatus_STOPPED, withRuntime: true,
			wantUpdated: map[string]string{expectedMLRunID: "COMPLETED"},
		},
		{
			name: "FAILED finalizes derived MLRun as FAILED", state: essmsgpb.WorkUnitStatus_FAILED, withRuntime: true,
			wantUpdated: map[string]string{expectedMLRunID: "FAILED"},
		},
		{
			name: "no runtime context finalizes stored MLRun", state: essmsgpb.WorkUnitStatus_COMPLETED, storedURL: storedURL,
			wantUpdated: map[string]string{"stored-run-1": "COMPLETED"},
		},
		{
			name: "no runtime context and no stored URL skips", state: essmsgpb.WorkUnitStatus_COMPLETED,
			wantUpdated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)

			if err := rule.OnExecutionStateChanged(context.Background(), storeWithCurrentEID("1"), lifecycleEvent(tc.state, tc.withRuntime), managedXprofArtifact(tc.storedURL)); err != nil {
				t.Fatalf("OnExecutionStateChanged() error = %v", err)
			}
			if diff := cmp.Diff(tc.wantUpdated, mlClient.updatedRuns); diff != "" {
				t.Errorf("updated runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestManagedXprofRule_OnExecutionSuperseded(t *testing.T) {
	tests := []struct {
		name        string
		storedURL   string
		wantUpdated map[string]string
	}{
		{
			name:        "finalizes MLRun from stored console URL",
			storedURL:   hypercomputeclient.ConsoleURL(testLocation, "real-active-mlrun-999", testProject),
			wantUpdated: map[string]string{"real-active-mlrun-999": "COMPLETED"},
		},
		{
			name:        "finalizes MLRun from stored runs-segment URL",
			storedURL:   "https://console.cloud.google.com/cluster-director/locations/us-central1/runs/legacy-run-1?project=test-proj",
			wantUpdated: map[string]string{"legacy-run-1": "COMPLETED"},
		},
		{
			name:        "skips artifact without stored MLRun URL",
			wantUpdated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)

			if err := rule.OnExecutionSuperseded(context.Background(), &storage.MockStorage{}, 10, 20, 1, managedXprofArtifact(tc.storedURL)); err != nil {
				t.Fatalf("OnExecutionSuperseded() error = %v", err)
			}
			if diff := cmp.Diff(tc.wantUpdated, mlClient.updatedRuns); diff != "" {
				t.Errorf("updated runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
