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

func TestDecodeYAMLDocuments(t *testing.T) {
	tests := []struct {
		name     string
		rendered string
		want     []map[string]any
		wantErr  bool
	}{
		{name: "empty stream", rendered: "", want: nil},
		{
			name:     "skips empty and comment-only documents",
			rendered: "---\n# Source: chart/templates/a.yaml\n---\nkind: A\nmetadata:\n  name: a\n---\n\n---\nkind: B\n",
			want:     []map[string]any{{"kind": "A", "metadata": map[string]any{"name": "a"}}, {"kind": "B"}},
		},
		{name: "invalid YAML", rendered: "kind: A\n  bad: indent: here\n", wantErr: true},
		{
			name:     "JSON document with numbers",
			rendered: `{"kind": "A", "spec": {"replicas": 2, "ratio": 0.5}}`,
			want:     []map[string]any{{"kind": "A", "spec": map[string]any{"replicas": float64(2), "ratio": 0.5}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeYAMLDocuments(tt.rendered)
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeYAMLDocuments() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("decodeYAMLDocuments() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
