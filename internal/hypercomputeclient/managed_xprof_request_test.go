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

package hypercomputeclient

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestResolveClusterPath(t *testing.T) {
	tests := []struct {
		name, cluster, project, location, want string
	}{
		{name: "bare name", cluster: "c1", project: "p", location: "l", want: "projects/p/locations/l/clusters/c1"},
		{name: "already a path", cluster: "projects/p/locations/l/clusters/c1", project: "q", location: "m", want: "projects/p/locations/l/clusters/c1"},
		{name: "missing project", cluster: "c1", location: "l", want: "c1"},
		{name: "missing location", cluster: "c1", project: "p", want: "c1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveClusterPath(tc.cluster, tc.project, tc.location); got != tc.want {
				t.Errorf("ResolveClusterPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildManagedXprofCreateOptions pins the CreateMLRun request (the payload the
// legacy reconciler on main sent): artifacts keyed by "gcs_path", the xprof tool,
// GKE orchestrator and JobSet workload details with the full cluster path.
func TestBuildManagedXprofCreateOptions(t *testing.T) {
	created := time.Date(2026, 10, 28, 16, 26, 5, 0, time.UTC)
	workload := ManagedXprofWorkload{
		ClusterPath:  "projects/p/locations/us-central1/clusters/c1",
		Namespace:    "cpu-queue",
		JobSetName:   "xmc-operation-1-2",
		CreationTime: created,
	}
	labels := BuildMLRunLabels(1, 2)
	got := BuildManagedXprofCreateOptions(workload, "run-group", labels, ManagedXprofArtifacts("gs://bucket/folder"))
	want := &CreateMLRunOptions{
		RunGroup:     "run-group",
		Tools:        []map[string]any{{"xprof": map[string]any{}}},
		Labels:       map[string]string{"experiment_id": "1", "workunit_id": "2", "created_by": "xmanager"},
		Artifacts:    map[string]string{"gcs_path": "gs://bucket/folder"},
		Orchestrator: "GKE",
		WorkloadDetails: &GKEWorkloadDetails{
			Kind:       "JobSet",
			Cluster:    "projects/p/locations/us-central1/clusters/c1",
			Namespace:  "cpu-queue",
			ID:         "xmc-operation-1-2",
			CreateTime: "2026-10-28T16:26:05Z",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BuildManagedXprofCreateOptions() mismatch (-want +got):\n%s", diff)
	}
}

func TestFormatWorkloadCreateTime(t *testing.T) {
	if got := FormatWorkloadCreateTime(time.Time{}); got != "" {
		t.Errorf("FormatWorkloadCreateTime(zero) = %q, want \"\"", got)
	}
}
