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

package bridge

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestParseHelpers(t *testing.T) {
	spec, dp, en := ParseSpecFromUnstructured(nil)
	if !dp || !en || spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		t.Errorf("unexpected defaults: spec=%+v, dp=%v, en=%v", spec, dp, en)
	}

	raw := map[string]any{
		"enabled":            true,
		"deletionProtection": false,
		"deletionPolicy":     "Orphan",
		"componentType":      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		"version":            "1.2.3",
		"description":        "desc",
		"wrapperRef": map[string]any{
			"group": "g", "version": "v", "kind": "k", "name": "n", "namespace": "ns",
		},
		"manifests": []any{"yaml-1", 123, "yaml-2"},
		"parameters": map[string]any{
			"topicName":   "xmc-metrics",
			"port":        8080,
			"bufferBytes": float64(1048576),
			"rate":        float64(3.14),
			"enabled":     true,
			"replica":     int64(3),
			"ignored":     nil,
		},
	}
	spec, dp, en = ParseSpecFromUnstructured(raw)
	wantSpec := &componentspb.ComponentSpec{
		Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Version:        "1.2.3",
		Description:    "desc",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:        "g",
			CrdVersion:      "v",
			CrdKind:         "k",
			TargetName:      "n",
			TargetNamespace: "ns",
		},
		Manifests: []string{"yaml-1", "yaml-2"},
		Parameters: map[string]string{
			"topicName":   "xmc-metrics",
			"port":        "8080",
			"bufferBytes": "1048576",
			"rate":        "3.14",
			"enabled":     "true",
			"replica":     "3",
		},
	}
	if dp || !en || !proto.Equal(spec, wantSpec) {
		t.Errorf("ParseSpecFromUnstructured() = %+v (dp=%v, en=%v), want %+v (dp=false, en=true)", spec, dp, en, wantSpec)
	}

	rawCamel := map[string]any{
		"enabled":       true,
		"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		"wrapperRef": map[string]any{
			"crdGroup": "clickhouse.altinity.com", "crdVersion": "v1", "crdKind": "ClickHouseInstallation",
			"targetName": "chi-cluster", "targetNamespace": "clickhouse-ns",
		},
	}
	specCamel, _, _ := ParseSpecFromUnstructured(rawCamel)
	wr := specCamel.GetWrapperRef()
	wantWrapperRef := &componentspb.WrapperRef{
		CrdGroup:        "clickhouse.altinity.com",
		CrdVersion:      "v1",
		CrdKind:         "ClickHouseInstallation",
		TargetName:      "chi-cluster",
		TargetNamespace: "clickhouse-ns",
	}
	if !proto.Equal(wr, wantWrapperRef) {
		t.Errorf("ParseSpecFromUnstructured() wrapperRef = %+v, want %+v", wr, wantWrapperRef)
	}

	rawDelete := map[string]any{
		"deletionPolicy": "DELETION_POLICY_ORPHAN",
	}
	specDel, _, _ := ParseSpecFromUnstructured(rawDelete)
	if specDel.GetDeletionPolicy() != componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		t.Errorf("expected DELETION_POLICY_ORPHAN, got %v", specDel.GetDeletionPolicy())
	}

	if ParseConditionsFromUnstructured(nil) != nil || ParseConditionsFromUnstructured(map[string]any{"conditions": "inv"}) != nil {
		t.Error("expected nil for empty/invalid conditions")
	}

	conds := ParseConditionsFromUnstructured(map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "observedGeneration": float64(2), "lastTransitionTime": "2026-08-24T00:00:00Z"},
			map[string]any{"type": "Reconciling", "status": "True", "observedGeneration": "invalid", "lastTransitionTime": "invalid-date"},
			"not-a-map",
		},
	})
	if len(conds) != 2 || conds[0].ObservedGeneration != 2 || conds[0].Status != "True" || conds[1].ObservedGeneration != 0 {
		t.Errorf("unexpected conditions: %+v", conds)
	}
	if ParseObservedGenerationFromUnstructured(nil) != 0 || ParseObservedGenerationFromUnstructured(map[string]any{"observedGeneration": int64(3)}) != 3 {
		t.Error("unexpected ParseObservedGenerationFromUnstructured result")
	}
}

func TestParseSpecFromUnstructured_Dependencies(t *testing.T) {
	tests := []struct {
		name string
		spec any
		want []string
	}{
		{name: "nil spec", spec: nil, want: nil},
		{name: "dependencies absent", spec: map[string]any{"version": "v1.0.0"}, want: nil},
		{name: "dependencies not a list", spec: map[string]any{"dependencies": "redpanda"}, want: nil},
		{name: "empty list", spec: map[string]any{"dependencies": []any{}}, want: nil},
		{
			name: "single dependency",
			spec: map[string]any{"dependencies": []any{"redpanda"}},
			want: []string{"redpanda"},
		},
		{
			name: "canonicalizes, dedupes, and preserves declaration order",
			spec: map[string]any{"dependencies": []any{" redpanda ", "components/auth", "redpanda", "components/redpanda"}},
			want: []string{"redpanda", "auth"},
		},
		{
			name: "keeps malformed strings verbatim for CMS validation and drops non-string entries",
			spec: map[string]any{"dependencies": []any{"", "  ", "/", float64(1), nil, map[string]any{"name": "x"}, "metrics"}},
			want: []string{"", "  ", "/", "metrics"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, _, _ := ParseSpecFromUnstructured(tc.spec)
			if diff := cmp.Diff(tc.want, spec.GetDependencies()); diff != "" {
				t.Errorf("ParseSpecFromUnstructured().GetDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseParameters(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]string
	}{
		{
			name: "nil input map",
			in:   nil,
			want: nil,
		},
		{
			name: "missing parameters key",
			in:   map[string]any{"other": "value"},
			want: nil,
		},
		{
			name: "empty parameters map",
			in:   map[string]any{"parameters": map[string]any{}},
			want: nil,
		},
		{
			name: "only nil values in parameters",
			in:   map[string]any{"parameters": map[string]any{"key": nil}},
			want: nil,
		},
		{
			name: "mixed types with large float64 integers",
			in: map[string]any{
				"parameters": map[string]any{
					"str":       "hello",
					"largeInt":  float64(1048576),
					"zeroFloat": float64(0),
					"decimal":   float64(12.34),
					"intVal":    int(42),
					"int64Val":  int64(9999),
					"boolTrue":  true,
					"boolFalse": false,
					"skipped":   nil,
				},
			},
			want: map[string]string{
				"str":       "hello",
				"largeInt":  "1048576",
				"zeroFloat": "0",
				"decimal":   "12.34",
				"intVal":    "42",
				"int64Val":  "9999",
				"boolTrue":  "true",
				"boolFalse": "false",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseParameters(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseParameters() len = %d, want %d (got: %+v, want: %+v)", len(got), len(tc.want), got, tc.want)
			}
			for k, wantV := range tc.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("parseParameters()[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestExtractHelpers(t *testing.T) {
	m := map[string]any{
		"int64":        int64(42),
		"int":          int(100),
		"int32":        int32(150),
		"float64":      float64(200.5),
		"jsonNumInt":   json.Number("300"),
		"jsonNumBad":   json.Number("not_a_number"),
		"stringInt":    "400",
		"stringBad":    "bad",
		"boolVal":      true,
		"strVal":       "hello",
		"emptyWrapper": "not-a-map",
		"emptyList":    "not-a-list",
	}

	if extractInt(m, "int64") != 42 {
		t.Errorf("extractInt int64 failed")
	}
	if extractInt(m, "int") != 100 {
		t.Errorf("extractInt int failed")
	}
	if extractInt(m, "int32") != 150 {
		t.Errorf("extractInt int32 failed")
	}
	if extractInt(m, "float64") != 200 {
		t.Errorf("extractInt float64 failed")
	}
	if extractInt(m, "jsonNumInt") != 300 {
		t.Errorf("extractInt jsonNumInt failed")
	}
	if extractInt(m, "jsonNumBad") != 0 {
		t.Errorf("extractInt jsonNumBad failed")
	}
	if extractInt(m, "stringInt") != 400 {
		t.Errorf("extractInt stringInt failed")
	}
	if extractInt(m, "stringBad") != 0 {
		t.Errorf("extractInt stringBad failed")
	}
	if extractInt(m, "boolVal") != 0 {
		t.Errorf("extractInt boolVal failed")
	}
	if extractInt(m, "nonexistent") != 0 {
		t.Errorf("extractInt nonexistent failed")
	}

	if extractStr(m, "strVal") != "hello" {
		t.Errorf("extractStr strVal failed")
	}
	if extractStr(m, "int") != "" {
		t.Errorf("extractStr int failed")
	}
	if extractStr(m, "nonexistent") != "" {
		t.Errorf("extractStr nonexistent failed")
	}

	if !extractBool(m, "boolVal", false) {
		t.Errorf("extractBool boolVal failed")
	}
	if !extractBool(m, "nonexistent", true) {
		t.Errorf("extractBool nonexistent default true failed")
	}
	if extractBool(m, "strVal", false) {
		t.Errorf("extractBool strVal default false failed")
	}

	if parseManifests(m) != nil {
		t.Errorf("parseManifests not-a-list failed")
	}
	if parseWrapperRef(m) != nil {
		t.Errorf("parseWrapperRef not-a-map failed")
	}
}

func TestParseWrapperRef(t *testing.T) {
	if ref := ParseWrapperRef(nil, "default-ns"); ref.Namespace != "default-ns" {
		t.Errorf("expected default namespace for nil spec: %+v", ref)
	}

	spec := map[string]any{
		"wrapperRef": map[string]any{
			"group":                    "ray.io",
			"version":                  "v1",
			"kind":                     "RayCluster",
			"name":                     "my-ray",
			"namespace":                "",
			"observedGenerationPath":   "status.obsGen",
			"readyConditionPath":       "status.conditions[0].status",
			"expectedReadyValue":       "True",
			"endpointJsonPath":         "status.url",
			"connectionSecretJsonPath": "status.secret",
		},
	}
	ref := ParseWrapperRef(spec, "fallback-ns")
	wantRef := WrapperRef{
		Group:                    "ray.io",
		Version:                  "v1",
		Kind:                     "RayCluster",
		Name:                     "my-ray",
		Namespace:                "fallback-ns",
		ObservedGenerationPath:   "status.obsGen",
		ReadyConditionPath:       "status.conditions[0].status",
		ExpectedReadyValue:       "True",
		EndpointJSONPath:         "status.url",
		ConnectionSecretJSONPath: "status.secret",
	}
	if ref != wantRef {
		t.Errorf("ParseWrapperRef() = %+v, want %+v", ref, wantRef)
	}

	specCamel := map[string]any{
		"wrapperRef": map[string]any{
			"crdGroup":                 "clickhouse.altinity.com",
			"crdVersion":               "v1",
			"crdKind":                  "ClickHouseInstallation",
			"targetName":               "chi-cluster",
			"targetNamespace":          "clickhouse-ns",
			"observedGenerationPath":   "status.observedGeneration",
			"readyConditionPath":       "status.ready",
			"expectedReadyValue":       "Ready",
			"endpointJsonPath":         "status.endpoint",
			"connectionSecretJsonPath": "status.secretName",
		},
	}
	refCamel := ParseWrapperRef(specCamel, "default-ns")
	wantCamel := WrapperRef{
		Group:                    "clickhouse.altinity.com",
		Version:                  "v1",
		Kind:                     "ClickHouseInstallation",
		Name:                     "chi-cluster",
		Namespace:                "clickhouse-ns",
		ObservedGenerationPath:   "status.observedGeneration",
		ReadyConditionPath:       "status.ready",
		ExpectedReadyValue:       "Ready",
		EndpointJSONPath:         "status.endpoint",
		ConnectionSecretJSONPath: "status.secretName",
	}
	if refCamel != wantCamel {
		t.Errorf("ParseWrapperRef(camelCase) = %+v, want %+v", refCamel, wantCamel)
	}
}

func TestParseWrapperRef_KeyAliases(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
		want WrapperRef
	}{
		{name: "missing wrapperRef", spec: map[string]any{}, want: WrapperRef{}},
		{name: "wrapperRef not a map", spec: map[string]any{"wrapperRef": "chi-cluster"}, want: WrapperRef{}},
		{
			name: "camelCase key wins over alias",
			spec: map[string]any{"wrapperRef": map[string]any{
				"crdKind": "ClickHouseInstallation", "kind": "Ignored",
				"targetName": "chi-cluster", "name": "ignored",
				"targetNamespace": "clickhouse-ns", "namespace": "ignored-ns",
			}},
			want: WrapperRef{Kind: "ClickHouseInstallation", Name: "chi-cluster", Namespace: "clickhouse-ns"},
		},
		{
			name: "empty camelCase key falls back to alias",
			spec: map[string]any{"wrapperRef": map[string]any{
				"crdGroup": "", "group": "ray.io", "targetNamespace": "", "namespace": "ray-ns",
			}},
			want: WrapperRef{Group: "ray.io", Namespace: "ray-ns"},
		},
		{
			name: "snake_case JSONPath aliases",
			spec: map[string]any{"wrapperRef": map[string]any{
				"name":                        "my-ray",
				"observed_generation_path":    "status.obsGen",
				"ready_condition_path":        "status.ready",
				"expected_ready_value":        "True",
				"endpoint_json_path":          "status.url",
				"connection_secret_json_path": "status.secret",
			}},
			want: WrapperRef{
				Name:                     "my-ray",
				Namespace:                "default-ns",
				ObservedGenerationPath:   "status.obsGen",
				ReadyConditionPath:       "status.ready",
				ExpectedReadyValue:       "True",
				EndpointJSONPath:         "status.url",
				ConnectionSecretJSONPath: "status.secret",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, ParseWrapperRef(tt.spec, "default-ns")); diff != "" {
				t.Errorf("ParseWrapperRef() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseAccessFromUnstructured(t *testing.T) {
	tests := []struct {
		name   string
		status any
		want   *AccessStatus
	}{
		{name: "nil status", status: nil, want: nil},
		{name: "missing access", status: map[string]any{"state": "ENABLED"}, want: nil},
		{name: "empty envVars", status: map[string]any{"access": map[string]any{"envVars": map[string]any{}}}, want: nil},
		{
			name:   "unstructured map[string]any envVars",
			status: map[string]any{"access": map[string]any{"envVars": map[string]any{"ENDPOINT": "http://ch:8123", "PORT": "8123", "bad": 123}}},
			want:   &AccessStatus{EnvVars: map[string]string{"ENDPOINT": "http://ch:8123", "PORT": "8123"}},
		},
		{
			name:   "typed map[string]string envVars",
			status: map[string]any{"access": map[string]any{"envVars": map[string]string{"HOST": "ch.xmc"}}},
			want:   &AccessStatus{EnvVars: map[string]string{"HOST": "ch.xmc"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, ParseAccessFromUnstructured(tt.status)); diff != "" {
				t.Errorf("ParseAccessFromUnstructured() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
