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

package k8sclient

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const testComponentID = "6200370836252645701"

func TestComponentSnapshotUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    componentSnapshot
		wantErr bool
	}{
		{name: "bare_generation", input: `3`, want: componentSnapshot{Generation: 3}},
		{
			name:  "object_string_id",
			input: `{"generation": 3, "componentId": "6200370836252645701"}`,
			want:  componentSnapshot{Generation: 3, ComponentID: testComponentID},
		},
		{
			name:  "object_integer_id",
			input: `{"generation": 3, "componentId": 6200370836252645701}`,
			want:  componentSnapshot{Generation: 3, ComponentID: testComponentID},
		},
		{
			name:  "object_non_canonical_id",
			input: `{"generation": 3, "componentId": "+0042"}`,
			want:  componentSnapshot{Generation: 3, ComponentID: "42"},
		},
		{name: "object_null_id", input: `{"generation": 3, "componentId": null}`, want: componentSnapshot{Generation: 3}},
		{name: "object_empty_string_id", input: `{"generation": 3, "componentId": ""}`, want: componentSnapshot{Generation: 3}},
		{name: "object_unknown_field", input: `{"generaton": 3, "componentId": "42"}`, wantErr: true},
		{name: "object_without_id", input: `{"generation": 3}`, want: componentSnapshot{Generation: 3}},
		{name: "object_id_only", input: `{"componentId": "42"}`, want: componentSnapshot{ComponentID: "42"}},
		{name: "zero_id", input: `{"generation": 3, "componentId": "0"}`, wantErr: true},
		{name: "negative_id", input: `{"generation": 3, "componentId": -1}`, wantErr: true},
		{name: "non_numeric_id", input: `{"generation": 3, "componentId": "abc"}`, wantErr: true},
		{name: "float_id", input: `{"generation": 3, "componentId": 1.5}`, wantErr: true},
		{name: "string_generation", input: `"3"`, wantErr: true},
		{name: "malformed_object", input: `{"generation": }`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got componentSnapshot
			err := json.Unmarshal([]byte(tt.input), &got)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("json.Unmarshal(%s) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("json.Unmarshal(%s) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestExtractComponentID(t *testing.T) {
	tests := []struct {
		name   string
		status map[string]any
		want   string
	}{
		{name: "string", status: map[string]any{"componentId": testComponentID}, want: testComponentID},
		{name: "int64", status: map[string]any{"componentId": int64(42)}, want: "42"},
		{name: "json_number", status: map[string]any{"componentId": json.Number(testComponentID)}, want: testComponentID},
		{name: "absent", status: map[string]any{}, want: ""},
		{name: "empty_string", status: map[string]any{"componentId": ""}, want: ""},
		{name: "non_numeric", status: map[string]any{"componentId": "abc"}, want: ""},
		{name: "float", status: map[string]any{"componentId": float64(42)}, want: ""},
		{name: "zero", status: map[string]any{"componentId": "0"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &unstructured.Unstructured{Object: map[string]any{"status": tt.status}}
			if got := extractComponentID(comp); got != tt.want {
				t.Errorf("extractComponentID(%v) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
	if got := extractComponentID(nil); got != "" {
		t.Errorf("extractComponentID(nil) = %q, want empty", got)
	}
}
