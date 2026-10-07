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

package crreader

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsDisabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		comp *unstructured.Unstructured
		want bool
	}{
		{
			name: "nil component is disabled",
			comp: nil,
			want: true,
		},
		{
			name: "nil object is disabled",
			comp: &unstructured.Unstructured{},
			want: true,
		},
		{
			name: "empty object map is disabled",
			comp: &unstructured.Unstructured{Object: map[string]any{}},
			want: true,
		},
		{
			name: "enabled component is not disabled",
			comp: baselineComponent(),
			want: false,
		},
		{
			name: "spec.enabled false is disabled",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, false, "spec", "enabled")
				return u
			}(),
			want: true,
		},
		{
			name: "status.state COMPONENT_STATE_DISABLED is disabled",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, StateDisabled, "status", "state")
				return u
			}(),
			want: true,
		},
		{
			name: "status.state COMPONENT_STATE_DISABLING is disabled",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, StateDisabling, "status", "state")
				return u
			}(),
			want: true,
		},
		{
			name: "status.state COMPONENT_STATE_DELETED is disabled",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, StateDeleted, "status", "state")
				return u
			}(),
			want: true,
		},
		{
			name: "malformed status.state type defaults to disabled when spec.enabled is true",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, int64(42), "status", "state")
				return u
			}(),
			want: true,
		},
		{
			name: "malformed spec.enabled type defaults to disabled",
			comp: func() *unstructured.Unstructured {
				u := baselineComponent()
				_ = unstructured.SetNestedField(u.Object, "not-a-bool", "spec", "enabled")
				return u
			}(),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDisabled(tt.comp); got != tt.want {
				t.Errorf("IsDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpecEnabledAndStatusState(t *testing.T) {
	t.Parallel()

	enabled, ok, err := SpecEnabled(nil)
	if err != nil || ok || enabled {
		t.Errorf("SpecEnabled(nil) = (%v, %v, %v), want (false, false, nil)", enabled, ok, err)
	}
	state, err := StatusState(nil)
	if err != nil || state != "" {
		t.Errorf("StatusState(nil) = (%q, %v), want (\"\", nil)", state, err)
	}

	comp := baselineComponent()
	enabled, ok, err = SpecEnabled(comp)
	if err != nil || !ok || !enabled {
		t.Errorf("SpecEnabled(comp) = (%v, %v, %v), want (true, true, nil)", enabled, ok, err)
	}
	state, err = StatusState(comp)
	if err != nil || state != "COMPONENT_STATE_ENABLED" {
		t.Errorf("StatusState(comp) = (%q, %v), want (\"COMPONENT_STATE_ENABLED\", nil)", state, err)
	}
}

func TestParametersAndAccessEnvVars(t *testing.T) {
	t.Parallel()

	if got, err := Parameters(nil); err != nil || got != nil {
		t.Errorf("Parameters(nil) = (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := AccessEnvVars(nil); err != nil || got != nil {
		t.Errorf("AccessEnvVars(nil) = (%v, %v), want (nil, nil)", got, err)
	}

	comp := baselineComponent()
	wantParams := map[string]string{
		"host": "clickhouse-from-spec.xmc.svc.cluster.local",
		"port": "9000",
	}
	gotParams, err := Parameters(comp)
	if err != nil {
		t.Fatalf("Parameters() unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantParams, gotParams); diff != "" {
		t.Errorf("Parameters() mismatch (-want +got):\n%s", diff)
	}

	wantEnv := map[string]string{
		"ENDPOINT": "clickhouse-live.xmc.svc.cluster.local",
		"PORT":     "9000",
	}
	gotEnv, err := AccessEnvVars(comp)
	if err != nil {
		t.Fatalf("AccessEnvVars() unexpected error: %v", err)
	}
	if diff := cmp.Diff(wantEnv, gotEnv); diff != "" {
		t.Errorf("AccessEnvVars() mismatch (-want +got):\n%s", diff)
	}

	malformed := &unstructured.Unstructured{
		Object: map[string]any{
			"spec": map[string]any{
				"parameters": "not-a-map",
			},
			"status": "not-a-map",
		},
	}
	if _, err := Parameters(malformed); err == nil {
		t.Errorf("Parameters(malformed) succeeded, want error")
	}
	if _, err := AccessEnvVars(malformed); err == nil {
		t.Errorf("AccessEnvVars(malformed) succeeded, want error")
	}

	missingField := &unstructured.Unstructured{Object: map[string]any{}}
	if got, err := nestedStringMap(missingField, "spec", "parameters"); err != nil || got != nil {
		t.Errorf("nestedStringMap(missing) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestCoerceStringMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  any
		want map[string]string
	}{
		{
			name: "nil input",
			raw:  nil,
			want: nil,
		},
		{
			name: "non-map input",
			raw:  "not-a-map",
			want: nil,
		},
		{
			name: "empty map[string]any",
			raw:  map[string]any{},
			want: nil,
		},
		{
			name: "empty map[string]string",
			raw:  map[string]string{},
			want: nil,
		},
		{
			name: "clones map[string]string",
			raw:  map[string]string{"a": "1", "b": "2"},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "coerces mixed scalar types and omits nil",
			raw: map[string]any{
				"str":    "hello",
				"number": json.Number("42"),
				"int":    123,
				"int64":  int64(456),
				"float":  float64(1000000),
				"bool":   true,
				"slice":  []any{"ignored"},
				"nilVal": nil,
			},
			want: map[string]string{
				"str":    "hello",
				"number": "42",
				"int":    "123",
				"int64":  "456",
				"float":  "1000000",
				"bool":   "true",
				"slice":  "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CoerceStringMap(tt.raw)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("CoerceStringMap() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
