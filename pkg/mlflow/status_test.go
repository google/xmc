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

package mlflow

import (
	"context"
	"net/http/httptest"
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestMapWorkUnitTerminalState(t *testing.T) {
	tests := []struct {
		name   string
		state  essmsgpb.WorkUnitStatus_WorkUnitState
		want   string
		wantOK bool
	}{
		{name: "completed", state: essmsgpb.WorkUnitStatus_COMPLETED, want: "FINISHED", wantOK: true},
		{name: "failed", state: essmsgpb.WorkUnitStatus_FAILED, want: "FAILED", wantOK: true},
		{name: "stopped", state: essmsgpb.WorkUnitStatus_STOPPED, want: "KILLED", wantOK: true},
		{name: "running", state: essmsgpb.WorkUnitStatus_RUNNING, want: "", wantOK: false},
		{name: "restarting", state: essmsgpb.WorkUnitStatus_RESTARTING, want: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MapWorkUnitTerminalState(tc.state)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("MapWorkUnitTerminalState(%v) = (%q, %v), want (%q, %v)", tc.state, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestComputeParentRunStatus(t *testing.T) {
	tests := []struct {
		name    string
		states  []essmsgpb.WorkUnitStatus_WorkUnitState
		want    string
		wantAll bool
	}{
		{name: "empty", states: nil, want: "", wantAll: false},
		{name: "active work unit remains", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.WorkUnitStatus_RUNNING}, want: "", wantAll: false},
		{name: "all stopped", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_STOPPED, essmsgpb.WorkUnitStatus_STOPPED}, want: "KILLED", wantAll: true},
		{name: "all failed", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_FAILED, essmsgpb.WorkUnitStatus_FAILED}, want: "FAILED", wantAll: true},
		{name: "all completed", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.WorkUnitStatus_COMPLETED}, want: "FINISHED", wantAll: true},
		{name: "mixed completed and stopped", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_COMPLETED, essmsgpb.WorkUnitStatus_STOPPED}, want: "FINISHED", wantAll: true},
		{name: "mixed failed and stopped", states: []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_FAILED, essmsgpb.WorkUnitStatus_STOPPED}, want: "FINISHED", wantAll: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, all := ComputeParentRunStatus(tc.states)
			if got != tc.want || all != tc.wantAll {
				t.Errorf("ComputeParentRunStatus(%v) = (%q, %v), want (%q, %v)", tc.states, got, all, tc.want, tc.wantAll)
			}
		})
	}
}

func TestClient_UpdateRunStatus_AndReopenParent(t *testing.T) {
	ctx := context.Background()
	srv := newFakeMLflowServer("")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	client, err := NewClient(&Config{TrackingURI: ts.URL}, ts.Client())
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	expID, err := client.EnsureExperiment(ctx, "exp-status")
	if err != nil {
		t.Fatalf("EnsureExperiment() error: %v", err)
	}
	parentID, err := client.EnsureParentRun(ctx, expID, 77, RunLinks{ExperimentTitle: "exp-status"})
	if err != nil {
		t.Fatalf("EnsureParentRun() error: %v", err)
	}
	childID, err := client.EnsureChildRun(ctx, expID, parentID, 77, 1, 1, RunLinks{ExperimentTitle: "exp-status"})
	if err != nil {
		t.Fatalf("EnsureChildRun() error: %v", err)
	}

	// Simulate client writing FINISHED at process exit, then XMC overriding with FAILED + end_time.
	if err := client.UpdateRunStatus(ctx, childID, "FINISHED", 1000); err != nil {
		t.Fatalf("UpdateRunStatus(FINISHED) error: %v", err)
	}
	if err := client.UpdateRunStatus(ctx, childID, "FAILED", 2500); err != nil {
		t.Fatalf("UpdateRunStatus(FAILED) error: %v", err)
	}
	if got := srv.runs[childID].Info.Status; got != "FAILED" {
		t.Errorf("child status = %q, want FAILED", got)
	}

	// Close parent run, then reopen on restart via EnsureParentRun without end_time.
	if err := client.UpdateRunStatus(ctx, parentID, "FAILED", 2500); err != nil {
		t.Fatalf("UpdateRunStatus(parent FAILED) error: %v", err)
	}
	srv.sawEndTime = false
	reopenedID, err := client.EnsureParentRun(ctx, expID, 77, RunLinks{ExperimentTitle: "exp-status"})
	if err != nil || reopenedID != parentID {
		t.Fatalf("EnsureParentRun(reopen) = (%q, %v), want %q", reopenedID, err, parentID)
	}
	if got := srv.runs[parentID].Info.Status; got != "RUNNING" {
		t.Errorf("reopened parent status = %q, want RUNNING", got)
	}
	if srv.sawEndTime {
		t.Errorf("EnsureParentRun(reopen) unexpectedly sent end_time")
	}
}
