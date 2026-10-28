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

package componentconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
)

func baselineBigQueryCR() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata":   map[string]any{"name": "bigquery", "namespace": "xmc"},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"parameters":    map[string]any{"projectId": "spec-proj", "dataset": "spec_dataset", "table": "spec_table"},
			},
			"status": map[string]any{
				"state":  "COMPONENT_STATE_ENABLED",
				"access": map[string]any{"envVars": map[string]any{"PROJECT_ID": "live-proj", "DATASET": "live_dataset", "TABLE": "live_table"}},
			},
		},
	}
}

func modifyCR(mutator func(u *unstructured.Unstructured)) *crreadertest.FakeGetter {
	u := baselineBigQueryCR()
	mutator(u)
	return &crreadertest.FakeGetter{Component: u}
}

func crSource(mutator func(u *unstructured.Unstructured)) Source {
	return Source{Enabled: true, Namespace: "xmc", FallbackProjectID: "fallback-proj", Getter: modifyCR(mutator)}
}

func TestResolveOptions(t *testing.T) {
	notFoundGetter := &crreadertest.FakeGetter{Err: apierrors.NewNotFound(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "bigquery")}
	envWant := xmcbigquery.Options{ProjectID: "env-proj", Dataset: "env_dataset", Table: "env_table"}
	liveWant := xmcbigquery.Options{ProjectID: "live-proj", Dataset: "live_dataset", Table: "live_table"}

	tests := []struct {
		name    string
		setup   func(t *testing.T)
		src     Source
		want    xmcbigquery.Options
		wantErr bool
	}{
		{name: "disabled source uses static env options", src: Source{Enabled: false}, want: envWant},
		{name: "enabled source with nil getter returns hard error", src: Source{Enabled: true}, wantErr: true},
		{name: "NotFound CR falls back to static env options", src: Source{Enabled: true, Getter: notFoundGetter}, want: envWant},
		{name: "non-NotFound API error returns hard error", src: Source{Enabled: true, Getter: &crreadertest.FakeGetter{Err: errors.New("forbidden")}}, wantErr: true},
		{name: "spec.enabled=false CR falls back to static env options", src: crSource(func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, false, "spec", "enabled")
		}), want: envWant},
		{name: "status.state=COMPONENT_STATE_DISABLED falls back to static env options", src: crSource(func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, "COMPONENT_STATE_DISABLED", "status", "state")
		}), want: envWant},
		{name: "NotFound CR with unconfigured static env returns ErrNotConfigured", setup: func(t *testing.T) { t.Setenv("BIGQUERY_DATASET", "") }, src: Source{Enabled: true, Getter: notFoundGetter}, wantErr: true},
		{name: "disabled CR with unconfigured static env returns ErrNotConfigured", setup: func(t *testing.T) { t.Setenv("BIGQUERY_DATASET", "") }, src: crSource(func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, false, "spec", "enabled")
		}), wantErr: true},
		{name: "resolves from status.access.envVars taking precedence over spec.parameters", src: crSource(func(*unstructured.Unstructured) {}), want: liveWant},
		{name: "resolves from spec.parameters when status.access.envVars is absent", src: crSource(func(u *unstructured.Unstructured) { unstructured.RemoveNestedField(u.Object, "status", "access") }), want: xmcbigquery.Options{ProjectID: "spec-proj", Dataset: "spec_dataset", Table: "spec_table"}},
		{
			name:  "falls back to FallbackProjectID and defaults when CR omits coordinates",
			setup: func(t *testing.T) { t.Setenv("BIGQUERY_PROJECT_ID", ""); t.Setenv("GOOGLE_CLOUD_PROJECT", "") },
			src: crSource(func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "status", "access")
				_ = unstructured.SetNestedMap(u.Object, map[string]any{}, "spec", "parameters")
			}),
			want: xmcbigquery.Options{ProjectID: "fallback-proj", Dataset: xmcbigquery.DefaultDataset, Table: xmcbigquery.DefaultTable},
		},
		{name: "malformed spec.parameters returns hard error", src: crSource(func(u *unstructured.Unstructured) {
			u.Object["spec"] = map[string]any{"enabled": true, "parameters": "bad"}
		}), wantErr: true},
		{name: "malformed status.access.envVars returns hard error", src: crSource(func(u *unstructured.Unstructured) {
			u.Object["status"] = map[string]any{"state": "COMPONENT_STATE_ENABLED", "access": map[string]any{"envVars": "bad"}}
		}), wantErr: true},
		{
			name: "invalid dataset identifier in CR returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "status", "access")
				_ = unstructured.SetNestedField(u.Object, "bad-dataset", "spec", "parameters", "dataset")
			}),
			wantErr: true,
		},
		{
			name:  "missing project ID everywhere returns hard error",
			setup: func(t *testing.T) { t.Setenv("BIGQUERY_PROJECT_ID", ""); t.Setenv("GOOGLE_CLOUD_PROJECT", "") },
			src: Source{
				Enabled: true, Namespace: "xmc",
				Getter: modifyCR(func(u *unstructured.Unstructured) {
					unstructured.RemoveNestedField(u.Object, "status", "access")
					_ = unstructured.SetNestedMap(u.Object, map[string]any{}, "spec", "parameters")
				}),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BIGQUERY_PROJECT_ID", "env-proj")
			t.Setenv("GOOGLE_CLOUD_PROJECT", "")
			t.Setenv("BIGQUERY_DATASET", "env_dataset")
			t.Setenv("BIGQUERY_TABLE", "env_table")
			if tt.setup != nil {
				tt.setup(t)
			}
			got, err := ResolveOptions(context.Background(), tt.src)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && strings.Contains(tt.name, "ErrNotConfigured") && !errors.Is(err, ErrNotConfigured) {
				t.Errorf("ResolveOptions() error = %v, want %v", err, ErrNotConfigured)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, *got); diff != "" {
					t.Errorf("ResolveOptions() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestOptionsFingerprint(t *testing.T) {
	t.Parallel()
	if got := OptionsFingerprint(nil); got != "" {
		t.Errorf("OptionsFingerprint(nil) = %q, want empty string", got)
	}
	base := &xmcbigquery.Options{ProjectID: "proj-a", Dataset: "xmc_metrics", Table: "xmc_metrics"}
	baseFP := OptionsFingerprint(base)
	if baseFP == "" {
		t.Fatal("OptionsFingerprint(base) returned empty string")
	}
	if got := OptionsFingerprint(base); got != baseFP {
		t.Errorf("OptionsFingerprint(base) = %q, want deterministic %q", got, baseFP)
	}
	for _, variant := range []*xmcbigquery.Options{
		{ProjectID: "proj-b", Dataset: base.Dataset, Table: base.Table},
		{ProjectID: base.ProjectID, Dataset: "other_dataset", Table: base.Table},
		{ProjectID: base.ProjectID, Dataset: base.Dataset, Table: "other_table"},
	} {
		if got := OptionsFingerprint(variant); got == baseFP {
			t.Errorf("OptionsFingerprint(%+v) = %q, want distinct fingerprint from base", variant, got)
		}
	}
}
