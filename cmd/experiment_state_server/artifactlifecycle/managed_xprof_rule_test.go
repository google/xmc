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
	"net/http"
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

var (
	errHypercompute = errors.New("hypercompute unavailable")
	errAlreadyExist = &hypercomputeclient.APIError{StatusCode: http.StatusConflict}
)

// fakeMLRunClient returns MLRun resources without a "url" field, so the rule falls
// back to the deterministic console URL. Errors are injected per operation; a label
// error only fails label-stamping updates and a phase error only phase updates.
type fakeMLRunClient struct {
	createErr      error
	getErr         error
	labelErr       error
	phaseErr       error
	adoptionLabels map[string]any
	createdRuns    map[string]string
	updatedRuns    map[string]string
	labeledRuns    []string
}

func newFakeMLRunClient() *fakeMLRunClient {
	return &fakeMLRunClient{
		createdRuns: make(map[string]string),
		updatedRuns: make(map[string]string),
	}
}

func (f *fakeMLRunClient) CreateMLRun(_ context.Context, name, _, runPhase string, _ *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createdRuns[name] = runPhase
	return map[string]any{"name": name}, nil
}

func (f *fakeMLRunClient) GetMLRun(_ context.Context, name string) (map[string]any, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	labels := f.adoptionLabels
	if labels == nil {
		labels = map[string]any{"created_by": "xmanager"}
	}
	return map[string]any{"name": name, "labels": labels}, nil
}

func (f *fakeMLRunClient) UpdateMLRun(_ context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	if opts.RunPhase != "" {
		if f.phaseErr != nil {
			return nil, f.phaseErr
		}
		f.updatedRuns[name] = opts.RunPhase
	}
	if len(opts.Labels) > 0 {
		if f.labelErr != nil {
			return nil, f.labelErr
		}
		f.labeledRuns = append(f.labeledRuns, name)
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

func executionName(eid string) string {
	return "experiments/10/workUnits/20/executions/" + eid
}

func storeWithCurrentEID(eid string) *storage.MockStorage {
	return &storage.MockStorage{
		WorkUnit: &essmsgpb.WorkUnit{
			Name:                     "experiments/10/workUnits/20",
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: executionName(eid)},
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

func executionWithMLRunURL(eid, mlRunURL string) *essmsgpb.WorkUnitExecution {
	return &essmsgpb.WorkUnitExecution{
		Name:     executionName(eid),
		Metadata: &essmsgpb.WorkUnitExecutionMetadata{MlrunUrl: mlRunURL},
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
		setup       func(*fakeMLRunClient)
		wantErr     error // nil means success; errAny means any error.
		wantCreated map[string]string
		wantUpdated map[string]string
		wantURL     string // "" means no artifact or execution write expected.
	}{
		{
			name:        "creates MLRun and persists URL on artifact and execution for matching EID",
			currentEID:  "1",
			withRuntime: true,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
			wantUpdated: map[string]string{},
			wantURL:     wantURL,
		},
		{
			name:        "closes MLRun and writes nothing when execution is superseded",
			currentEID:  "2",
			withRuntime: true,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
			wantUpdated: map[string]string{expectedMLRunID: "COMPLETED"},
		},
		{
			name:        "superseded close failure is returned for retry",
			currentEID:  "2",
			withRuntime: true,
			setup:       func(f *fakeMLRunClient) { f.phaseErr = errHypercompute },
			wantErr:     errHypercompute,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
			wantUpdated: map[string]string{},
		},
		{
			name:        "skips MLRun creation without runtime context",
			currentEID:  "1",
			wantCreated: map[string]string{},
			wantUpdated: map[string]string{},
		},
		{
			name:        "create failure is returned and no URL is persisted",
			currentEID:  "1",
			withRuntime: true,
			setup:       func(f *fakeMLRunClient) { f.createErr = errHypercompute },
			wantErr:     errHypercompute,
			wantCreated: map[string]string{},
			wantUpdated: map[string]string{},
		},
		{
			name:        "label stamp failure after create is returned and no URL is persisted",
			currentEID:  "1",
			withRuntime: true,
			setup:       func(f *fakeMLRunClient) { f.labelErr = errHypercompute },
			wantErr:     errHypercompute,
			wantCreated: map[string]string{expectedMLRunID: "ACTIVE"},
			wantUpdated: map[string]string{},
		},
		{
			name:        "AlreadyExists adopts a fully adopted run as success",
			currentEID:  "1",
			withRuntime: true,
			setup: func(f *fakeMLRunClient) {
				f.createErr = errAlreadyExist
				f.adoptionLabels = map[string]any{"created_by": "xmanager", hypercomputeclient.ListExistingSessionsOnlyLabel: "true"}
			},
			wantCreated: map[string]string{},
			wantUpdated: map[string]string{},
			wantURL:     wantURL,
		},
		{
			name:        "AlreadyExists get failure is returned and no URL is persisted",
			currentEID:  "1",
			withRuntime: true,
			setup: func(f *fakeMLRunClient) {
				f.createErr = errAlreadyExist
				f.getErr = errHypercompute
			},
			wantErr:     errHypercompute,
			wantCreated: map[string]string{},
			wantUpdated: map[string]string{},
		},
		{
			name:        "AlreadyExists adoption update failure is returned and no URL is persisted",
			currentEID:  "1",
			withRuntime: true,
			setup: func(f *fakeMLRunClient) {
				f.createErr = errAlreadyExist
				f.labelErr = errHypercompute
			},
			wantErr:     errHypercompute,
			wantCreated: map[string]string{},
			wantUpdated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			if tc.setup != nil {
				tc.setup(mlClient)
			}
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)
			store := storeWithCurrentEID(tc.currentEID)

			err := rule.OnExecutionStateChanged(context.Background(), store, lifecycleEvent(essmsgpb.WorkUnitStatus_RUNNING, tc.withRuntime), managedXprofArtifact(""))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("OnExecutionStateChanged() error = %v, want %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantCreated, mlClient.createdRuns); diff != "" {
				t.Errorf("created runs mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantUpdated, mlClient.updatedRuns); diff != "" {
				t.Errorf("updated run phases mismatch (-want +got):\n%s", diff)
			}
			assertArtifactUpdate(t, store, tc.wantURL)
			assertExecutionMLRunURL(t, store, tc.wantURL)
		})
	}
}

// TestManagedXprofRule_RetryAfterLabelFailureIsIdempotent replays an event whose
// first attempt created the MLRun but failed to stamp its label: the retry adopts
// the run through AlreadyExists, re-stamps the label, and persists the URL.
func TestManagedXprofRule_RetryAfterLabelFailureIsIdempotent(t *testing.T) {
	mlClient := newFakeMLRunClient()
	mlClient.labelErr = errHypercompute
	rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)
	store := storeWithCurrentEID("1")
	event := lifecycleEvent(essmsgpb.WorkUnitStatus_RUNNING, true)

	if err := rule.OnExecutionStateChanged(context.Background(), store, event, managedXprofArtifact("")); !errors.Is(err, errHypercompute) {
		t.Fatalf("first OnExecutionStateChanged() error = %v, want %v", err, errHypercompute)
	}
	mlClient.labelErr = nil
	mlClient.createErr = errAlreadyExist
	if err := rule.OnExecutionStateChanged(context.Background(), store, event, managedXprofArtifact("")); err != nil {
		t.Fatalf("retried OnExecutionStateChanged() error = %v", err)
	}
	if diff := cmp.Diff([]string{expectedMLRunID}, mlClient.labeledRuns); diff != "" {
		t.Errorf("labeled runs mismatch (-want +got):\n%s", diff)
	}
	wantURL := hypercomputeclient.ConsoleURL(testLocation, expectedMLRunID, testProject)
	assertArtifactUpdate(t, store, wantURL)
	assertExecutionMLRunURL(t, store, wantURL)
}

func TestManagedXprofRule_NilClientReturnsError(t *testing.T) {
	rule := artifactlifecycle.NewManagedXprofRule(nil, testProject, testLocation)
	store := storeWithCurrentEID("1")

	err := rule.OnExecutionStateChanged(context.Background(), store, lifecycleEvent(essmsgpb.WorkUnitStatus_RUNNING, true), managedXprofArtifact(""))
	if !errors.Is(err, artifactlifecycle.ErrMLRunClientUnavailable) {
		t.Errorf("OnExecutionStateChanged() error = %v, want %v", err, artifactlifecycle.ErrMLRunClientUnavailable)
	}
	err = rule.OnExecutionSuperseded(context.Background(), store, 10, 20, 1, managedXprofArtifact(""))
	if !errors.Is(err, artifactlifecycle.ErrMLRunClientUnavailable) {
		t.Errorf("OnExecutionSuperseded() error = %v, want %v", err, artifactlifecycle.ErrMLRunClientUnavailable)
	}
	assertArtifactUpdate(t, store, "")
	assertExecutionMLRunURL(t, store, "")
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

// assertExecutionMLRunURL checks the metadata.mlrun_url written on the current
// execution of work unit 20; "" means no execution write is expected.
func assertExecutionMLRunURL(t *testing.T, store *storage.MockStorage, wantURL string) {
	t.Helper()
	exec, written := store.UpdatedWorkUnitExecutions[20]
	if wantURL == "" {
		if written {
			t.Errorf("unexpected execution write: %v", exec)
		}
		return
	}
	if !written {
		t.Fatal("expected an execution mlrun_url write, got none")
	}
	if got := exec.GetMetadata().GetMlrunUrl(); got != wantURL {
		t.Errorf("execution mlrun_url = %q, want %q", got, wantURL)
	}
	if got := exec.GetName(); got != executionName("1") {
		t.Errorf("execution name = %q, want %q", got, executionName("1"))
	}
}

func TestManagedXprofRule_TerminalState(t *testing.T) {
	recordedURL := hypercomputeclient.ConsoleURL(testLocation, "recorded-run-1", testProject)
	tests := []struct {
		name        string
		state       essmsgpb.WorkUnitStatus_WorkUnitState
		withRuntime bool
		executions  []*essmsgpb.WorkUnitExecution
		setup       func(*fakeMLRunClient)
		wantErr     error
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
			name: "create failure is returned before finalization", state: essmsgpb.WorkUnitStatus_COMPLETED, withRuntime: true,
			setup:       func(f *fakeMLRunClient) { f.createErr = errHypercompute },
			wantErr:     errHypercompute,
			wantUpdated: map[string]string{},
		},
		{
			name: "phase update failure is returned", state: essmsgpb.WorkUnitStatus_COMPLETED, withRuntime: true,
			setup:       func(f *fakeMLRunClient) { f.phaseErr = errHypercompute },
			wantErr:     errHypercompute,
			wantUpdated: map[string]string{},
		},
		{
			name: "no runtime context finalizes MLRun recorded on the event execution", state: essmsgpb.WorkUnitStatus_COMPLETED,
			executions: []*essmsgpb.WorkUnitExecution{
				executionWithMLRunURL("2", hypercomputeclient.ConsoleURL(testLocation, "other-eid-run", testProject)),
				executionWithMLRunURL("1", recordedURL),
			},
			wantUpdated: map[string]string{"recorded-run-1": "COMPLETED"},
		},
		{
			name: "no runtime context and no recorded URL skips", state: essmsgpb.WorkUnitStatus_COMPLETED,
			executions:  []*essmsgpb.WorkUnitExecution{executionWithMLRunURL("1", "")},
			wantUpdated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			if tc.setup != nil {
				tc.setup(mlClient)
			}
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)
			store := storeWithCurrentEID("1")
			store.ListWorkUnitExecutionsResp = tc.executions

			err := rule.OnExecutionStateChanged(context.Background(), store, lifecycleEvent(tc.state, tc.withRuntime), managedXprofArtifact(""))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("OnExecutionStateChanged() error = %v, want %v", err, tc.wantErr)
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
		executions  []*essmsgpb.WorkUnitExecution
		listErr     error
		phaseErr    error
		wantErr     error
		wantUpdated map[string]string
	}{
		{
			name: "closes exactly the MLRun recorded on the archived execution",
			executions: []*essmsgpb.WorkUnitExecution{
				executionWithMLRunURL("2", hypercomputeclient.ConsoleURL(testLocation, "new-eid-run", testProject)),
				executionWithMLRunURL("1", hypercomputeclient.ConsoleURL(testLocation, "old-eid-run", testProject)),
			},
			wantUpdated: map[string]string{"old-eid-run": "COMPLETED"},
		},
		{
			name: "closes MLRun from a runs-segment URL",
			executions: []*essmsgpb.WorkUnitExecution{
				executionWithMLRunURL("1", "https://console.cloud.google.com/cluster-director/locations/us-central1/runs/legacy-run-1?project=test-proj"),
			},
			wantUpdated: map[string]string{"legacy-run-1": "COMPLETED"},
		},
		{
			name:        "skips execution without recorded MLRun URL",
			executions:  []*essmsgpb.WorkUnitExecution{executionWithMLRunURL("1", "")},
			wantUpdated: map[string]string{},
		},
		{
			name:        "skips when the superseded execution is not found",
			executions:  []*essmsgpb.WorkUnitExecution{executionWithMLRunURL("2", hypercomputeclient.ConsoleURL(testLocation, "new-eid-run", testProject))},
			wantUpdated: map[string]string{},
		},
		{
			name:        "list executions failure is returned",
			listErr:     errHypercompute,
			wantErr:     errHypercompute,
			wantUpdated: map[string]string{},
		},
		{
			name:        "close failure is returned",
			executions:  []*essmsgpb.WorkUnitExecution{executionWithMLRunURL("1", hypercomputeclient.ConsoleURL(testLocation, "old-eid-run", testProject))},
			phaseErr:    errHypercompute,
			wantErr:     errHypercompute,
			wantUpdated: map[string]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mlClient := newFakeMLRunClient()
			mlClient.phaseErr = tc.phaseErr
			rule := artifactlifecycle.NewManagedXprofRule(mlClient, testProject, testLocation)
			store := &storage.MockStorage{ListWorkUnitExecutionsResp: tc.executions, ListWorkUnitExecutionsErr: tc.listErr}

			err := rule.OnExecutionSuperseded(context.Background(), store, 10, 20, 1, managedXprofArtifact(hypercomputeclient.ConsoleURL(testLocation, "shared-artifact-run", testProject)))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("OnExecutionSuperseded() error = %v, want %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantUpdated, mlClient.updatedRuns); diff != "" {
				t.Errorf("updated runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
