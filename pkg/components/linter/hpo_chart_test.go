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

package linter_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

const (
	hpoDeploymentFile     = "hpo/templates/deployment.yaml"
	hpoServiceAccountFile = "hpo/templates/serviceaccount.yaml"
	hpoComponentFile      = "hpo/templates/component.yaml"
)

// studyControllerWorkload is the subset of the rendered xmc-study-controller Deployment that the
// hpo chart contract pins.
type studyControllerWorkload struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas int `yaml:"replicas"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
			Spec struct {
				ServiceAccountName            string `yaml:"serviceAccountName"`
				TerminationGracePeriodSeconds int    `yaml:"terminationGracePeriodSeconds"`
				Containers                    []struct {
					Image string `yaml:"image"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type studyControllerSummary struct {
	Kind, Name, ServiceAccount, Image string
	Replicas                          int
	TerminationGracePeriodSeconds     int
	PodLabels                         map[string]string
}

func hpoChartDir(t *testing.T) string {
	t.Helper()
	return resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a", "components", "hpo"))
}

func summarizeStudyController(t *testing.T, manifest string) studyControllerSummary {
	t.Helper()
	var w studyControllerWorkload
	if err := yaml.Unmarshal([]byte(manifest), &w); err != nil {
		t.Fatalf("failed to unmarshal rendered Deployment: %v", err)
	}
	got := studyControllerSummary{
		Kind: w.Kind, Name: w.Metadata.Name, ServiceAccount: w.Spec.Template.Spec.ServiceAccountName,
		Replicas: w.Spec.Replicas, PodLabels: w.Spec.Template.Metadata.Labels,
		TerminationGracePeriodSeconds: w.Spec.Template.Spec.TerminationGracePeriodSeconds,
	}
	if len(w.Spec.Template.Spec.Containers) == 1 {
		got.Image = w.Spec.Template.Spec.Containers[0].Image
	}
	return got
}

// TestHPOChart_BridgeReleaseRendersWorkload renders the hpo chart as the xmc-cmp-hpo release that
// xmc-bridge installs: it must render the xmc-study-controller Deployment and ServiceAccount, and
// not the Component CR.
func TestHPOChart_BridgeReleaseRendersWorkload(t *testing.T) {
	t.Parallel()
	rendered, err := renderChartWithValues(hpoChartDir(t), "xmc-cmp-hpo", map[string]any{
		"hpoDeployment": map[string]any{"enabled": true, "image": "registry.example.com/xmc/study-controller-dev:v7"},
	})
	if err != nil {
		t.Fatalf("renderChartWithValues(xmc-cmp-hpo) failed: %v", err)
	}

	want := studyControllerSummary{
		Kind: "Deployment", Name: "xmc-study-controller", ServiceAccount: "xmc-study-controller",
		Image: "registry.example.com/xmc/study-controller-dev:v7", Replicas: 1,
		PodLabels: map[string]string{
			"app":                          "xmc-study-controller",
			"app.kubernetes.io/name":       "xmc-study-controller",
			"xmc.google.com/vizier-client": "true",
		},
	}
	if diff := cmp.Diff(want, summarizeStudyController(t, rendered[hpoDeploymentFile])); diff != "" {
		t.Errorf("xmc-study-controller Deployment mismatch (-want +got):\n%s", diff)
	}
	sa := rendered[hpoServiceAccountFile]
	if !strings.Contains(sa, "kind: ServiceAccount") || !strings.Contains(sa, "name: xmc-study-controller") {
		t.Errorf("expected ServiceAccount xmc-study-controller for xmc-cmp-hpo, got:\n%s", sa)
	}
	if strings.Contains(sa, "iam.gke.io/gcp-service-account") {
		t.Errorf("expected a plain ServiceAccount without a Workload Identity annotation, got:\n%s", sa)
	}
	if strings.Contains(rendered[hpoComponentFile], "kind:") {
		t.Errorf("expected component.yaml to be suppressed for xmc-cmp-hpo, got:\n%s", rendered[hpoComponentFile])
	}
	for _, f := range []string{hpoDeploymentFile, hpoServiceAccountFile} {
		assertWellFormedManifests(t, rendered[f])
	}
}

// TestHPOChart_ImageResolution covers the xmc-study-controller image precedence: the bridge-resolved
// hpoDeployment.image, then the default repository with global.imageTag, imageTag or latest.
func TestHPOChart_ImageResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{
			name:   "tagged bridge image is used verbatim",
			values: map[string]any{"global": map[string]any{"imageTag": "v1"}, "hpoDeployment": map[string]any{"image": "r.example.com/sc:v9"}},
			want:   "r.example.com/sc:v9",
		},
		{
			name:   "digest bridge image is used verbatim",
			values: map[string]any{"global": map[string]any{"imageTag": "v1"}, "hpoDeployment": map[string]any{"image": "r.example.com/sc@sha256:abc"}},
			want:   "r.example.com/sc@sha256:abc",
		},
		{
			name:   "untagged bridge image gets global imageTag",
			values: map[string]any{"global": map[string]any{"imageTag": "v1"}, "hpoDeployment": map[string]any{"image": "r.example.com:5000/sc"}},
			want:   "r.example.com:5000/sc:v1",
		},
		{
			name:   "default repository with imageTag",
			values: map[string]any{"imageTag": "v2"},
			want:   "us-docker.pkg.dev/xmc-prod/xmc/study-controller:v2",
		},
		{
			name:   "global imageTag wins over imageTag",
			values: map[string]any{"global": map[string]any{"imageTag": "v3"}, "imageTag": "v2"},
			want:   "us-docker.pkg.dev/xmc-prod/xmc/study-controller:v3",
		},
		{
			name: "default repository with latest",
			want: "us-docker.pkg.dev/xmc-prod/xmc/study-controller:latest",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := renderChartWithValues(hpoChartDir(t), "xmc-cmp-hpo", tc.values)
			if err != nil {
				t.Fatalf("renderChartWithValues(xmc-cmp-hpo) failed: %v", err)
			}
			if got := summarizeStudyController(t, rendered[hpoDeploymentFile]).Image; got != tc.want {
				t.Errorf("xmc-study-controller image = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHPOChart_TerminationGracePeriod checks that the grace period xmc-bridge derives from the
// Component drain timeout reaches the pod, and is omitted when unset.
func TestHPOChart_TerminationGracePeriod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values map[string]any
		want   int
	}{
		{name: "bridge grace period is rendered", values: map[string]any{"hpoDeployment": map[string]any{"terminationGracePeriodSeconds": 35}}, want: 35},
		{name: "unset grace period is omitted", values: map[string]any{"hpoDeployment": map[string]any{"enabled": true}}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := renderChartWithValues(hpoChartDir(t), "xmc-cmp-hpo", tc.values)
			if err != nil {
				t.Fatalf("renderChartWithValues(xmc-cmp-hpo) failed: %v", err)
			}
			dep := rendered[hpoDeploymentFile]
			if got := summarizeStudyController(t, dep).TerminationGracePeriodSeconds; got != tc.want {
				t.Errorf("terminationGracePeriodSeconds = %d, want %d", got, tc.want)
			}
			if tc.want == 0 && strings.Contains(dep, "terminationGracePeriodSeconds") {
				t.Errorf("expected terminationGracePeriodSeconds to be omitted, got:\n%s", dep)
			}
			assertWellFormedManifests(t, dep)
		})
	}
}

// TestHPOChart_ProfileReleaseRendersOnlyComponent renders the hpo chart outside an xmc-cmp-*
// release: only the Component CR is rendered, with spec.imageTag from imageTag.
func TestHPOChart_ProfileReleaseRendersOnlyComponent(t *testing.T) {
	t.Parallel()
	rendered, err := renderChartWithValues(hpoChartDir(t), "hpo", map[string]any{"enabled": true, "imageTag": "v4"})
	if err != nil {
		t.Fatalf("renderChartWithValues(hpo) failed: %v", err)
	}
	for _, f := range []string{hpoDeploymentFile, hpoServiceAccountFile} {
		if strings.Contains(rendered[f], "kind:") {
			t.Errorf("expected %s to render nothing outside xmc-cmp-* releases, got:\n%s", f, rendered[f])
		}
	}
	comp := rendered[hpoComponentFile]
	if !strings.Contains(comp, "kind: Component") || !strings.Contains(comp, `imageTag: "v4"`) {
		t.Errorf("expected Component CR with imageTag v4, got:\n%s", comp)
	}
}
