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

package hypercomputeclient

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBuildMLRunLabels(t *testing.T) {
	want := map[string]string{
		"experiment_id": "12",
		"workunit_id":   "3",
		"created_by":    "xmanager",
	}
	if diff := cmp.Diff(want, BuildMLRunLabels(12, 3)); diff != "" {
		t.Errorf("BuildMLRunLabels(12, 3) mismatch (-want +got):\n%s", diff)
	}
}

func TestWithExistingSessionsOnlyLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   map[string]string
	}{
		{name: "nil labels", labels: nil, want: map[string]string{ListExistingSessionsOnlyLabel: "true"}},
		{
			name:   "existing labels preserved",
			labels: map[string]string{"a": "b"},
			want:   map[string]string{"a": "b", ListExistingSessionsOnlyLabel: "true"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := WithExistingSessionsOnlyLabel(tc.labels)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("WithExistingSessionsOnlyLabel() mismatch (-want +got):\n%s", diff)
			}
			if _, mutated := tc.labels[ListExistingSessionsOnlyLabel]; mutated {
				t.Errorf("WithExistingSessionsOnlyLabel() mutated its input")
			}
		})
	}
}

func TestMLRunHasLabel(t *testing.T) {
	tests := []struct {
		name  string
		mlRun map[string]any
		want  bool
	}{
		{name: "nil mlRun", mlRun: nil, want: false},
		{name: "missing labels entry", mlRun: map[string]any{}, want: false},
		{name: "non-map labels entry", mlRun: map[string]any{"labels": "not-a-map"}, want: false},
		{name: "missing key in labels", mlRun: map[string]any{"labels": map[string]any{"other": "v"}}, want: false},
		{name: "mismatched value in labels", mlRun: map[string]any{"labels": map[string]any{"k": "wrong"}}, want: false},
		{name: "matching key and value", mlRun: map[string]any{"labels": map[string]any{"k": "v"}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MLRunHasLabel(tc.mlRun, "k", "v"); got != tc.want {
				t.Errorf("MLRunHasLabel(%v, k, v) = %v, want %v", tc.mlRun, got, tc.want)
			}
		})
	}
}

func TestMLRunOwnedByXManager(t *testing.T) {
	tests := []struct {
		name  string
		mlRun map[string]any
		want  bool
	}{
		{name: "no labels", mlRun: map[string]any{}, want: false},
		{name: "other owner", mlRun: map[string]any{"labels": map[string]any{"created_by": "someone"}}, want: false},
		{name: "owned", mlRun: map[string]any{"labels": map[string]any{"created_by": "xmanager"}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MLRunOwnedByXManager(tc.mlRun); got != tc.want {
				t.Errorf("MLRunOwnedByXManager(%v) = %v, want %v", tc.mlRun, got, tc.want)
			}
		})
	}
}

func TestMLRunAdoptionComplete(t *testing.T) {
	tests := []struct {
		name  string
		mlRun map[string]any
		want  bool
	}{
		{name: "owned without sessions label", mlRun: map[string]any{"labels": map[string]any{"created_by": "xmanager"}}, want: false},
		{name: "sessions label without ownership", mlRun: map[string]any{"labels": map[string]any{ListExistingSessionsOnlyLabel: "true"}}, want: false},
		{
			name:  "owned with sessions label",
			mlRun: map[string]any{"labels": map[string]any{"created_by": "xmanager", ListExistingSessionsOnlyLabel: "true"}},
			want:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MLRunAdoptionComplete(tc.mlRun); got != tc.want {
				t.Errorf("MLRunAdoptionComplete(%v) = %v, want %v", tc.mlRun, got, tc.want)
			}
		})
	}
}
