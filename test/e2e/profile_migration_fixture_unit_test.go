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

package e2e

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTallyWorkloadLogs(t *testing.T) {
	tests := []struct {
		name string
		logs string
		want logTally
	}{
		{name: "empty", logs: "", want: logTally{}},
		{name: "complete lines", logs: "WORKLOAD_OK\nWORKLOAD_OK seq=2\nWORKLOAD_FAIL seq=3 got=\n", want: logTally{ok: 2, fail: 1}}, //nolint:dupword // intentional consecutive log lines in test fixture
		{name: "partial last line ignored", logs: "WORKLOAD_OK\nWORKLOAD_FA", want: logTally{ok: 1}},
		{name: "unrelated lines ignored", logs: "curl: (6) Could not resolve host\nWORKLOAD_FAIL curl_exit=6\n  WORKLOAD_OK\n", want: logTally{fail: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, tallyWorkloadLogs(tt.logs), cmp.AllowUnexported(logTally{})); diff != "" {
				t.Errorf("tallyWorkloadLogs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkloadOKHosts(t *testing.T) {
	tests := []struct {
		name string
		logs string
		want []string
	}{
		{name: "empty", logs: "", want: nil},
		{
			name: "hosts aligned with successes",
			logs: "WORKLOAD_OK host=gw-a\nWORKLOAD_FAIL curl_exit=7\nWORKLOAD_OK seq=2\nWORKLOAD_OK host=gw-b extra\nWORKLOAD_OK host=\nWORKLOAD_OK host=partial",
			want: []string{"gw-a", "", "gw-b", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, workloadOKHosts(tt.logs)); diff != "" {
				t.Errorf("workloadOKHosts() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkloadFailureExcerpt(t *testing.T) {
	tests := []struct {
		name     string
		logs     string
		maxLines int
		want     string
	}{
		{name: "empty", logs: "", maxLines: 2, want: "first failures:\n\nlast lines:\n"},
		{
			name:     "caps failures and tail",
			logs:     "WORKLOAD_FAIL 1\nWORKLOAD_OK\nWORKLOAD_FAIL 2\nWORKLOAD_FAIL 3\nWORKLOAD_OK\n",
			maxLines: 2,
			want:     "first failures:\nWORKLOAD_FAIL 1\nWORKLOAD_FAIL 2\nlast lines:\nWORKLOAD_FAIL 3\nWORKLOAD_OK",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, workloadFailureExcerpt(tt.logs, tt.maxLines)); diff != "" {
				t.Errorf("workloadFailureExcerpt() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// baseProfileComponent returns a minimal rendered Component CR named name, as the profile chart
// emits it.
func baseProfileComponent(name string) map[string]any {
	return map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
		"metadata": map[string]any{"name": name, "labels": map[string]any{profileLabel: "stable-2026a", "app.kubernetes.io/name": name}},
		"spec":     map[string]any{"enabled": true, "version": "0.1.0", "chartVersion": "0.1.0"},
	}
}

func TestApplyProfileOverlay(t *testing.T) {
	overlay := profileOverlay{Profile: "stable-2026b", ImageTag: "2.53", ChartVersions: map[string]string{"clickhouse": "0.2.0"}}
	tests := []struct {
		name    string
		objs    []map[string]any
		want    []map[string]any
		wantErr bool
	}{
		{
			name: "rewrites label and versions",
			objs: []map[string]any{baseProfileComponent("clickhouse")},
			want: []map[string]any{{
				"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
				"metadata": map[string]any{"name": "clickhouse", "labels": map[string]any{profileLabel: "stable-2026b", "app.kubernetes.io/name": "clickhouse"}},
				"spec":     map[string]any{"enabled": true, "version": "0.2.0", "chartVersion": "0.2.0"},
			}},
		},
		{name: "component missing from overlay", objs: []map[string]any{baseProfileComponent("redpanda")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyProfileOverlay(tt.objs, overlay)
			if (err != nil) != tt.wantErr {
				t.Fatalf("applyProfileOverlay() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("applyProfileOverlay() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApplyProfileOverlayKeepsInput(t *testing.T) {
	objs := []map[string]any{baseProfileComponent("clickhouse")}
	if _, err := applyProfileOverlay(objs, profileOverlay{Profile: "stable-2026b", ChartVersions: map[string]string{"clickhouse": "0.2.0"}}); err != nil {
		t.Fatalf("applyProfileOverlay() unexpected error: %v", err)
	}
	if diff := cmp.Diff([]map[string]any{baseProfileComponent("clickhouse")}, objs); diff != "" {
		t.Errorf("applyProfileOverlay() mutated its input (-want +got):\n%s", diff)
	}
}
