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

package store_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestExtractDependencies(t *testing.T) {
	tests := []struct {
		name     string
		spec     *componentspb.ComponentSpec
		labels   map[string]string
		selfName string
		want     []string
	}{
		{
			name: "combined manifests, description, and labels",
			spec: &componentspb.ComponentSpec{
				Manifests: []string{
					`
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  annotations:
    xmc.dev/depends-on: "storage-service, components/auth-service"
`,
				},
				Description: `
This is the main worker controller.
@depends-on: telemetry-service, logging-service
`,
			},
			labels: map[string]string{
				"xmc.dev/dependencies": "kueue-operator, my-worker",
				"environment":          "production",
			},
			selfName: "my-worker",
			want: []string{
				"auth-service",
				"kueue-operator",
				"logging-service",
				"storage-service",
				"telemetry-service",
			},
		},
		{
			name:     "nil spec and nil labels",
			spec:     nil,
			labels:   nil,
			selfName: "self-comp",
			want:     []string{},
		},
		{
			name: "empty spec fields",
			spec: &componentspb.ComponentSpec{
				Manifests:   []string{},
				Description: "",
			},
			labels:   map[string]string{},
			selfName: "self-comp",
			want:     []string{},
		},
		{
			name: "self name filter with components prefix",
			spec: &componentspb.ComponentSpec{
				Description: "@depends-on: self-comp, dep-a",
			},
			labels: map[string]string{
				"xmc.dev/depends-on": "components/self-comp, dep-b",
			},
			selfName: "components/self-comp",
			want:     []string{"dep-a", "dep-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.ExtractDependencies(tt.spec, tt.labels, tt.selfName)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExtractDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractDependenciesFromManifests(t *testing.T) {
	tests := []struct {
		name      string
		manifests []string
		want      []string
	}{
		{
			name: "single manifest with annotations",
			manifests: []string{
				`
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    xmc.dev/depends-on: "dep-a, dep-b"
    xmc.dev/dependencies: "dep-c"
`,
			},
			want: []string{"dep-a, dep-b", "dep-c"},
		},
		{
			name: "multi-document YAML stream",
			manifests: []string{
				`
apiVersion: v1
kind: ConfigMap
metadata:
  name: cfg1
  annotations:
    xmc.dev/depends-on: "doc1-dep"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cfg2
  annotations:
    xmc.dev/dependencies: "doc2-dep"
---
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp1
spec:
  dependencies:
    - name: spec-dep-obj
    - spec-dep-str
`,
			},
			want: []string{
				"doc1-dep",
				"doc2-dep",
				"spec-dep-obj",
				"spec-dep-str",
			},
		},
		{
			name: "spec dependencies as comma string",
			manifests: []string{
				`
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: comp2
spec:
  dependencies: "string-dep-1, string-dep-2"
`,
			},
			want: []string{"string-dep-1, string-dep-2"},
		},
		{
			name: "manifest with line exceeding 64KB",
			manifests: []string{
				"metadata:\n  annotations:\n    xmc.dev/depends-on: large-dep\n# " + strings.Repeat("A", 70000) + "\n---\nmetadata:\n  annotations:\n    xmc.dev/dependencies: second-dep",
			},
			want: []string{"large-dep", "second-dep"},
		},
		{
			name: "empty and malformed manifests",
			manifests: []string{
				"",
				"   ",
				"invalid : : : yaml [}",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.ExtractDependenciesFromManifests(tt.manifests)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExtractDependenciesFromManifests() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractDependenciesFromDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want []string
	}{
		{
			name: "at depends-on directive",
			desc: "Some summary.\n@depends-on: auth-service, db-service\nDetails follow.",
			want: []string{"auth-service, db-service"},
		},
		{
			name: "plain depends-on directive",
			desc: "depends-on: redis-cache",
			want: []string{"redis-cache"},
		},
		{
			name: "xmc:depends-on directive",
			desc: "xmc:depends-on: envoy-gateway",
			want: []string{"envoy-gateway"},
		},
		{
			name: "xmc.dev/depends-on directive",
			desc: "xmc.dev/depends-on: kueue-manager",
			want: []string{"kueue-manager"},
		},
		{
			name: "dependencies directive variations",
			desc: "@dependencies: alpha\ndependencies: beta\nxmc:dependencies: gamma",
			want: []string{"alpha", "beta", "gamma"},
		},
		{
			name: "empty or no directives",
			desc: "Just a plain description without directives.",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.ExtractDependenciesFromDescription(tt.desc)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExtractDependenciesFromDescription() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractDependenciesFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   []string
	}{
		{
			name: "both dependency labels present",
			labels: map[string]string{
				"xmc.dev/depends-on":   "dep-1",
				"xmc.dev/dependencies": "dep-2, dep-3",
				"other/label":          "ignore",
			},
			want: []string{"dep-1", "dep-2, dep-3"},
		},
		{
			name:   "nil and empty labels",
			labels: nil,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.ExtractDependenciesFromLabels(tt.labels)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ExtractDependenciesFromLabels() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNormalizeDependencies(t *testing.T) {
	tests := []struct {
		name     string
		rawDeps  []string
		selfName string
		want     []string
	}{
		{
			name: "deduplicates, strips components prefix, cleans brackets/quotes and sorts",
			rawDeps: []string{
				`["components/dep-b", 'dep-a']`,
				"dep-c; dep-a",
				"components/dep-b\ndep-d",
				"self-component",
				"invalid_name!",
			},
			selfName: "components/self-component",
			want:     []string{"dep-a", "dep-b", "dep-c", "dep-d"},
		},
		{
			name:     "empty slice input",
			rawDeps:  []string{},
			selfName: "any",
			want:     []string{},
		},
		{
			name:     "all self or invalid",
			rawDeps:  []string{"self-comp", "components/self-comp", "", "---"},
			selfName: "self-comp",
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.NormalizeDependencies(tt.rawDeps, tt.selfName)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NormalizeDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
