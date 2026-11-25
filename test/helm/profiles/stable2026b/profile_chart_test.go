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

// Package stable2026b_test tests the deploy/profiles/stable-2026b meta-chart. The generic profile
// contract comes from profilecontract; this file holds only the stable-2026b expectations: its
// catalog, the profile label of its Component CRs, the component-specific values it accepts and
// rejects, and the metadata-only 1P catalog entries that reuse stable-2026a workload charts.
package stable2026b_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/test/helm/profiles/profilecontract"
)

// stable2026b describes the stable-2026b profile catalog, rendered in dependency order.
func stable2026b() profilecontract.Spec {
	return profilecontract.Spec{
		ChartRelPath: "deploy/profiles/stable-2026b",
		ProfileName:  "stable-2026b",
		Components:   []string{"redpanda", "clickhouse", "ingestion-gateway", "vizier", "hpo"},
	}
}

func TestStable2026b_ProfileContract(t *testing.T) {
	t.Parallel()
	profilecontract.Run(t, stable2026b())
}

// TestStable2026b_IsPreview checks that stable-2026b stays a preview profile until its release,
// so dev_env requires --allow-preview-profile to install it.
func TestStable2026b_IsPreview(t *testing.T) {
	t.Parallel()
	if got := profilecontract.ReleaseStatus(t, stable2026b()); got != profilecontract.StatusPreview {
		t.Errorf("profile.yaml status = %q, want %q", got, profilecontract.StatusPreview)
	}
}

func TestStable2026b_RendersCatalogWithImageTag(t *testing.T) {
	t.Parallel()
	profilecontract.AssertRendersComponents(t, stable2026b(), map[string]any{"components": map[string]any{"redpanda": map[string]any{"enabled": true},
		"clickhouse": map[string]any{"enabled": true}, "ingestion-gateway": map[string]any{"enabled": true, "imageTag": "v1.2.3"},
		"vizier": map[string]any{"enabled": true, "acknowledgeExperimental": true}, "hpo": map[string]any{"enabled": true, "acknowledgeExperimental": true, "imageTag": "v1.2.3"}}})
}

// TestStable2026b_LabelsComponentsWithProfile checks that every Component CR carries the profile
// label that makes xmc-bridge select the stable-2026b embedded charts.
func TestStable2026b_LabelsComponentsWithProfile(t *testing.T) {
	t.Parallel()
	s := stable2026b()
	docs := profilecontract.RenderDocs(t, profilecontract.LoadProfile(t, s), nil)
	for _, name := range s.Components {
		if got := profilecontract.Lookup(profilecontract.DocByName(t, docs, name), "metadata", "labels", "xmc.dev/profile"); got != "stable-2026b" {
			t.Errorf("%s label xmc.dev/profile = %v, want stable-2026b", name, got)
		}
	}
}

func TestStable2026b_OverridesMatchStandalone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component string
		overrides map[string]any
	}{
		{name: "redpanda overrides", component: "redpanda", overrides: map[string]any{"enabled": true, "namespace": "data",
			"deletionProtection": false, "deletionPolicy": "Orphan", "parameters": map[string]any{"kafkaPort": 19092}, "dependencies": []any{"clickhouse"}}},
		{name: "clickhouse overrides", component: "clickhouse", overrides: map[string]any{"enabled": true,
			"parameters": map[string]any{"host": "ch.example.internal", "httpPort": 18123}}},
		{name: "ingestion-gateway overrides", component: "ingestion-gateway", overrides: map[string]any{"enabled": true, "imageTag": "v9",
			"dependencies": []any{"redpanda"}, "parameters": map[string]any{"topicName": "custom", "extraKey": "extra"}}},
		{name: "vizier overrides", component: "vizier", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"endpoint": "vizier-custom.xmc.svc.cluster.local:8088"}}},
		{name: "vizier gRPC healthCheckEndpoint override", component: "vizier", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"healthCheckEndpoint": "grpc://vizier-service.xmc.svc.cluster.local:7077"}}},
		{name: "hpo overrides", component: "hpo", overrides: map[string]any{"enabled": true, "namespace": "hpo-ns", "imageTag": "v9",
			"deletionProtection": false, "deletionPolicy": "Orphan", "acknowledgeExperimental": true, "dependencies": []any{"vizier"}}},
		{name: "hpo maxTrialBudget override", component: "hpo", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"maxTrialBudget": "250"}}},
		{name: "hpo studyControllerServiceAccount override", component: "hpo", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"studyControllerServiceAccount": "xmc-study-controller@my-project.iam.gserviceaccount.com"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profilecontract.AssertMatchesStandalone(t, stable2026b(), tt.component, tt.overrides)
		})
	}
}

// TestStable2026b_ExperimentalComponentDefaults checks the catalog-fixed EXPERIMENTAL stability of
// vizier and hpo, and the hpo defaults an admin gets without overrides.
func TestStable2026b_ExperimentalComponentDefaults(t *testing.T) {
	t.Parallel()

	docs := profilecontract.RenderDocs(t, profilecontract.LoadProfile(t, stable2026b()), map[string]any{"global": map[string]any{"imageTag": "v2"}})
	tests := []struct {
		name      string
		component string
		field     string
		want      any
	}{
		{name: "vizier renders catalog EXPERIMENTAL stability", component: "vizier", field: "stability", want: "EXPERIMENTAL"},
		{name: "vizier omits acknowledgeExperimental", component: "vizier", field: "acknowledgeExperimental", want: nil},
		{name: "hpo renders catalog EXPERIMENTAL stability", component: "hpo", field: "stability", want: "EXPERIMENTAL"},
		{name: "hpo omits acknowledgeExperimental", component: "hpo", field: "acknowledgeExperimental", want: nil},
		{name: "hpo renders default maxTrialBudget", component: "hpo", field: "parameters", want: map[string]any{"maxTrialBudget": "1000"}},
		{name: "global imageTag reaches hpo", component: "hpo", field: "imageTag", want: "v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, profilecontract.Lookup(profilecontract.DocByName(t, docs, tt.component), "spec", tt.field)); diff != "" {
				t.Errorf("%s spec.%s mismatch (-want +got):\n%s", tt.component, tt.field, diff)
			}
		})
	}
}

// TestStable2026b_VizierHealthCheckEndpoint checks that the gRPC health probe is opt-in: by default
// vizier renders only endpoint (TCP probe), and healthCheckEndpoint is rendered only when set.
func TestStable2026b_VizierHealthCheckEndpoint(t *testing.T) {
	t.Parallel()

	const endpoint = "vizier-service.xmc.svc.cluster.local:7077"
	tests := []struct {
		name      string
		overrides map[string]any
		want      map[string]any
	}{
		{name: "default probes endpoint over TCP", overrides: nil, want: map[string]any{"endpoint": endpoint}},
		{name: "empty healthCheckEndpoint is omitted", overrides: vizierParameters(map[string]any{"healthCheckEndpoint": ""}),
			want: map[string]any{"endpoint": endpoint}},
		{name: "gRPC healthCheckEndpoint opt-in", overrides: vizierParameters(map[string]any{"healthCheckEndpoint": "grpc://" + endpoint}),
			want: map[string]any{"endpoint": endpoint, "healthCheckEndpoint": "grpc://" + endpoint}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			docs := profilecontract.RenderDocs(t, profilecontract.LoadProfile(t, stable2026b()), tt.overrides)
			got := profilecontract.Lookup(profilecontract.DocByName(t, docs, "vizier"), "spec", "parameters")
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("vizier spec.parameters mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func vizierParameters(params map[string]any) map[string]any {
	return profilecontract.ComponentOverrides("vizier", map[string]any{"enabled": true, "acknowledgeExperimental": true, "parameters": params})
}

func hpoMaxTrialBudget(value any) map[string]any {
	return profilecontract.ComponentOverrides("hpo", map[string]any{"parameters": map[string]any{"maxTrialBudget": value}})
}

func TestStable2026b_RejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]any
		wantErr   string
	}{
		{name: "workload key override", overrides: profilecontract.ComponentOverrides("ingestion-gateway", map[string]any{"deployWorkload": true}), wantErr: "deployWorkload"},
		{name: "unknown 3P parameter on redpanda", overrides: profilecontract.ComponentOverrides("redpanda", map[string]any{"parameters": map[string]any{"bogus": "1"}}), wantErr: "bogus"},
		{name: "stable-2026a platformProfile", overrides: map[string]any{"global": map[string]any{"platformProfile": "stable-2026a"}}, wantErr: "platformProfile"},
		{name: "workload key override on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"hpoDeployment": map[string]any{"image": "r/sc:v1"}}), wantErr: "hpoDeployment"},
		{name: "unknown 3P parameter on vizier", overrides: profilecontract.ComponentOverrides("vizier", map[string]any{"parameters": map[string]any{"unknownParam": "foo"}}), wantErr: "unknownParam"},
		{name: "unknown parameter on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"parameters": map[string]any{"unknownParam": "foo"}}), wantErr: "unknownParam"},
		{name: "stability override on vizier", overrides: profilecontract.ComponentOverrides("vizier", map[string]any{"stability": "STABLE"}), wantErr: "stability"},
		{name: "stability override on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"stability": "STABLE"}), wantErr: "stability"},
		{name: "boolean maxTrialBudget on hpo", overrides: hpoMaxTrialBudget(true), wantErr: "maxTrialBudget"},
		{name: "zero maxTrialBudget on hpo", overrides: hpoMaxTrialBudget(0), wantErr: "maxTrialBudget"},
		{name: "negative maxTrialBudget on hpo", overrides: hpoMaxTrialBudget(-1), wantErr: "maxTrialBudget"},
		{name: "exponent maxTrialBudget on hpo", overrides: hpoMaxTrialBudget("1e6"), wantErr: "maxTrialBudget"},
		{name: "ten-digit string maxTrialBudget on hpo", overrides: hpoMaxTrialBudget("2147483648"), wantErr: "maxTrialBudget"},
		{name: "int32 overflow maxTrialBudget on hpo", overrides: hpoMaxTrialBudget(2147483648), wantErr: "maxTrialBudget"},
		{name: "non-email studyControllerServiceAccount on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"parameters": map[string]any{"studyControllerServiceAccount": "xmc-study-controller"}}), wantErr: "studyControllerServiceAccount"},
		{name: "display-name studyControllerServiceAccount on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"parameters": map[string]any{"studyControllerServiceAccount": "SC <sc@p.iam.gserviceaccount.com>"}}), wantErr: "studyControllerServiceAccount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profilecontract.AssertRenderFails(t, stable2026b(), profilecontract.DefaultRelease, tt.overrides, tt.wantErr)
		})
	}
}

// chartIdentity is the part of a Chart.yaml that identifies the chart a release installs.
