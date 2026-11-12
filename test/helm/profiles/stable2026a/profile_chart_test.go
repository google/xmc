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

// Package stable2026a_test tests the deploy/profiles/stable-2026a meta-chart. The generic profile
// contract comes from profilecontract; this file holds only the stable-2026a expectations: its
// catalog, and the component-specific values it accepts, rejects and forwards. The tests live
// outside the chart directory so the Bazel test binary never lands inside the chart that Helm's
// loader reads.
package stable2026a_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/test/helm/profiles/profilecontract"
)

// stable2026a describes the stable-2026a profile: five catalog components, rendered in
// dependency order.
func stable2026a() profilecontract.Spec {
	return profilecontract.Spec{
		ChartRelPath: "deploy/profiles/stable-2026a",
		ProfileName:  "stable-2026a",
		Components:   []string{"redpanda", "clickhouse", "ingestion-gateway", "vizier", "hpo"},
	}
}

func TestStable2026a_ProfileContract(t *testing.T) {
	t.Parallel()
	profilecontract.Run(t, stable2026a())
}

func TestStable2026a_RendersCatalogWithImageTag(t *testing.T) {
	t.Parallel()
	profilecontract.AssertRendersComponents(t, stable2026a(), map[string]any{"components": map[string]any{"redpanda": map[string]any{"enabled": true},
		"clickhouse": map[string]any{"enabled": true}, "ingestion-gateway": map[string]any{"enabled": true, "imageTag": "v1.2.3"},
		"vizier": map[string]any{"enabled": true, "acknowledgeExperimental": true}, "hpo": map[string]any{"enabled": true, "acknowledgeExperimental": true, "imageTag": "v1.2.3"}}})
}

func TestStable2026a_OverridesMatchStandalone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component string
		overrides map[string]any
	}{
		{name: "redpanda overrides", component: "redpanda", overrides: map[string]any{"enabled": true, "namespace": "data",
			"deletionProtection": false, "deletionPolicy": "Orphan", "acknowledgeExperimental": true, "parameters": map[string]any{"kafkaPort": 19092}, "dependencies": []any{"clickhouse"}}},
		{name: "clickhouse overrides", component: "clickhouse", overrides: map[string]any{"enabled": true,
			"parameters": map[string]any{"host": "ch.example.internal", "httpPort": 18123}}},
		{name: "ingestion-gateway overrides", component: "ingestion-gateway", overrides: map[string]any{"enabled": true, "imageTag": "v9",
			"dependencies": []any{"redpanda"}, "parameters": map[string]any{"topicName": "custom", "extraKey": "extra"}}},
		{name: "vizier overrides", component: "vizier", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"endpoint": "vizier-custom.xmc.svc.cluster.local:8088"}}},
		{name: "hpo overrides", component: "hpo", overrides: map[string]any{"enabled": true, "namespace": "hpo-ns", "imageTag": "v9",
			"deletionProtection": false, "deletionPolicy": "Orphan", "acknowledgeExperimental": true, "dependencies": []any{"vizier"}}},
		{name: "hpo maxTrialBudget override", component: "hpo", overrides: map[string]any{"enabled": true, "acknowledgeExperimental": true,
			"parameters": map[string]any{"maxTrialBudget": "250"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profilecontract.AssertMatchesStandalone(t, stable2026a(), tt.component, tt.overrides)
		})
	}
}

func TestStable2026a_HonorsExplicitFalsyOverrides(t *testing.T) {
	t.Parallel()

	s := stable2026a()
	docs := profilecontract.RenderDocs(t, profilecontract.LoadProfile(t, s), map[string]any{"global": map[string]any{"imageTag": "v2"}, "components": map[string]any{
		"redpanda": map[string]any{"deletionProtection": false, "acknowledgeExperimental": false}, "ingestion-gateway": map[string]any{"enabled": true, "acknowledgeExperimental": true, "dependencies": []any{}}}})
	tests := []struct {
		name      string
		component string
		field     string
		want      any
	}{
		{name: "deletionProtection false wins over default true", component: "redpanda", field: "deletionProtection", want: false},
		{name: "acknowledgeExperimental false is rendered when explicitly set", component: "redpanda", field: "acknowledgeExperimental", want: false},
		{name: "acknowledgeExperimental true is rendered when explicitly set", component: "ingestion-gateway", field: "acknowledgeExperimental", want: true},
		{name: "untouched component keeps default deletionProtection", component: "clickhouse", field: "deletionProtection", want: true},
		{name: "untouched component renders catalog STABLE stability", component: "clickhouse", field: "stability", want: "STABLE"},
		{name: "untouched vizier renders catalog EXPERIMENTAL stability", component: "vizier", field: "stability", want: "EXPERIMENTAL"},
		{name: "untouched vizier omits acknowledgeExperimental", component: "vizier", field: "acknowledgeExperimental", want: nil},
		{name: "untouched hpo renders catalog EXPERIMENTAL stability", component: "hpo", field: "stability", want: "EXPERIMENTAL"},
		{name: "untouched hpo omits acknowledgeExperimental", component: "hpo", field: "acknowledgeExperimental", want: nil},
		{name: "untouched hpo renders default maxTrialBudget", component: "hpo", field: "parameters", want: map[string]any{"maxTrialBudget": "1000"}},
		{name: "empty dependencies list replaces defaults", component: "ingestion-gateway", field: "dependencies", want: nil},
		{name: "global imageTag reaches component", component: "ingestion-gateway", field: "imageTag", want: "v2"},
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

func hpoMaxTrialBudget(value any) map[string]any {
	return profilecontract.ComponentOverrides("hpo", map[string]any{"parameters": map[string]any{"maxTrialBudget": value}})
}

func TestStable2026a_RejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]any
		wantErr   string
	}{
		{name: "workload key override", overrides: profilecontract.ComponentOverrides("ingestion-gateway", map[string]any{"deployWorkload": true}), wantErr: "deployWorkload"},
		{name: "workload key override on hpo", overrides: profilecontract.ComponentOverrides("hpo", map[string]any{"hpoDeployment": map[string]any{"image": "r/sc:v1"}}), wantErr: "hpoDeployment"},
		{name: "unknown 3P parameter on redpanda", overrides: profilecontract.ComponentOverrides("redpanda", map[string]any{"parameters": map[string]any{"bogus": "1"}}), wantErr: "bogus"},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profilecontract.AssertRenderFails(t, stable2026a(), profilecontract.DefaultRelease, tt.overrides, tt.wantErr)
		})
	}
}

// TestStable2026a_EmptyValuesFileDefaults checks that an empty redpanda values.yaml renders the
// defaults of redpanda's component template, and still applies admin overrides.
func TestStable2026a_EmptyValuesFileDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]any
		want      map[string]any
	}{
		{name: "empty values.yaml renders template defaults",
			want: map[string]any{"enabled": false, "deletionProtection": true, "kafkaPort": "9092"}},
		{name: "empty values.yaml still applies overrides",
			overrides: profilecontract.ComponentOverrides("redpanda", map[string]any{"enabled": true, "deletionProtection": false, "parameters": map[string]any{"kafkaPort": 19092}}),
			want:      map[string]any{"enabled": true, "deletionProtection": false, "kafkaPort": "19092"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := stable2026a()
			ch := profilecontract.LoadProfileWithFileEdit(t, s, "components/redpanda/values.yaml", "", false)
			spec := profilecontract.Lookup(profilecontract.DocByName(t, profilecontract.RenderDocs(t, ch, tt.overrides), "redpanda"), "spec")
			got := map[string]any{"enabled": profilecontract.Lookup(spec, "enabled"), "deletionProtection": profilecontract.Lookup(spec, "deletionProtection"),
				"kafkaPort": profilecontract.Lookup(spec, "parameters", "kafkaPort")}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("redpanda spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
