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

// Package vizier_test renders the deploy/addons/hpo/vizier reference chart through the Helm SDK
// and checks the contract the `vizier` component and its clients rely on: the vizier-service gRPC
// endpoint, the KinD overlay, the opt-in NetworkPolicy and the database wiring. The tests live
// outside the chart directory so the Bazel test binary never lands inside the chart.
package vizier_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"xmanager_cloud/test/helm/profiles/profilecontract"
)

const (
	chartRelPath = "deploy/addons/hpo/vizier"
	namespace    = "xmc"
	kindValues   = "values-kind.yaml"
)

var lookup = profilecontract.Lookup

// postgresOverrides selects the PostgreSQL backend with the required connection values.
func postgresOverrides() map[string]any {
	return map[string]any{
		"database": map[string]any{
			"type":     "postgres",
			"postgres": map[string]any{"host": "10.0.0.5", "user": "xmc"},
		},
	}
}

// loadChart locates the chart with the profile checks' runfiles resolution and loads it through
// the Helm SDK.
func loadChart(t *testing.T) (*chart.Chart, string) {
	t.Helper()
	dir, err := profilecontract.ChartDir(chartRelPath)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("loader.Load(%s) failed: %v", dir, err)
	}
	return ch, dir
}

// mergedValues returns fresh values holding the values files in valuesFiles (relative to dir, later
// files winning), overridden by overrides as with helm --set. overrides is deep-copied, never
// modified.
func mergedValues(t *testing.T, dir string, overrides map[string]any, valuesFiles []string) map[string]any {
	t.Helper()
	raw, err := yaml.Marshal(overrides)
	if err != nil {
		t.Fatalf("yaml.Marshal(%v) failed: %v", overrides, err)
	}
	vals, err := chartutil.ReadValues(raw)
	if err != nil {
		t.Fatalf("chartutil.ReadValues failed: %v", err)
	}
	// CoalesceTables keeps the values already in vals, so files are merged from last to first.
	for _, f := range slices.Backward(valuesFiles) {
		fileVals, err := chartutil.ReadValuesFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("ReadValuesFile(%s) failed: %v", f, err)
		}
		vals = chartutil.CoalesceTables(vals, fileVals.AsMap())
	}
	return vals
}

// render renders the chart into the xmc namespace with valuesFiles and overrides.
func render(t *testing.T, overrides map[string]any, valuesFiles ...string) ([]map[string]any, error) {
	t.Helper()
	ch, dir := loadChart(t)
	opts := chartutil.ReleaseOptions{Name: "vizier", Namespace: namespace, Revision: 1, IsInstall: true}
	renderVals, err := chartutil.ToRenderValues(ch, mergedValues(t, dir, overrides, valuesFiles), opts, nil)
	if err != nil {
		t.Fatalf("ToRenderValues failed: %v", err)
	}
	rendered, err := engine.Render(ch, renderVals)
	if err != nil {
		return nil, err
	}
	return decodeAll(t, rendered), nil
}

// mustRender renders like render and fails the test on a render error.
func mustRender(t *testing.T, overrides map[string]any, valuesFiles ...string) []map[string]any {
	t.Helper()
	docs, err := render(t, overrides, valuesFiles...)
	if err != nil {
		t.Fatalf("render(%v, %v) failed: %v", overrides, valuesFiles, err)
	}
	return docs
}

// decodeAll decodes every non-empty YAML document of the rendered templates, in path order.
func decodeAll(t *testing.T, rendered map[string]string) []map[string]any {
	t.Helper()
	var docs []map[string]any
	for _, path := range slices.Sorted(maps.Keys(rendered)) {
		d, err := profilecontract.DecodeDocs(rendered[path])
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		docs = append(docs, d...)
	}
	return docs
}

// findDoc returns the rendered document of the given kind and name, or nil when there is none.
func findDoc(docs []map[string]any, kind, name string) map[string]any {
	for _, doc := range docs {
		if doc["kind"] == kind && lookup(doc, "metadata", "name") == name {
			return doc
		}
	}
	return nil
}

// mustFindDoc returns the rendered document of the given kind and name, failing when absent.
func mustFindDoc(t *testing.T, docs []map[string]any, kind, name string) map[string]any {
	t.Helper()
	doc := findDoc(docs, kind, name)
	if doc == nil {
		t.Fatalf("no rendered %s named %q", kind, name)
	}
	return doc
}

// kinds returns the sorted "Kind/name" of every rendered document.
func kinds(docs []map[string]any) []string {
	var got []string
	for _, doc := range docs {
		got = append(got, fmt.Sprintf("%v/%v", doc["kind"], lookup(doc, "metadata", "name")))
	}
	slices.Sort(got)
	return got
}

// asSlice returns the slice at the key path of doc, failing when it is absent or not a slice.
func asSlice(t *testing.T, doc any, keys ...string) []any {
	t.Helper()
	s, ok := lookup(doc, keys...).([]any)
	if !ok {
		t.Fatalf("%v is %T, want a list", keys, lookup(doc, keys...))
	}
	return s
}

// vizierContainer returns the vizier container of the rendered Deployment.
func vizierContainer(t *testing.T, docs []map[string]any) map[string]any {
	t.Helper()
	containers := asSlice(t, mustFindDoc(t, docs, "Deployment", "vizier"), "spec", "template", "spec", "containers")
	if len(containers) != 1 {
		t.Fatalf("Deployment vizier has %d containers, want 1", len(containers))
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container is %T, want a map", containers[0])
	}
	return container
}

// containerEnv returns the container's env entries keyed by name, without the name field.
func containerEnv(t *testing.T, container map[string]any) map[string]any {
	t.Helper()
	env := map[string]any{}
	for _, raw := range asSlice(t, container, "env") {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("env entry is %T, want a map", raw)
		}
		name, ok := entry["name"].(string)
		if !ok {
			t.Fatalf("env entry %v has no string name", entry)
		}
		rest := maps.Clone(entry)
		delete(rest, "name")
		env[name] = rest
	}
	return env
}

// value is the env entry of a literal value.
func value(v string) map[string]any {
	return map[string]any{"value": v}
}

func TestVizierChart_RenderedResources(t *testing.T) {
	t.Parallel()

	withPVC := []string{
		"Deployment/vizier",
		"PersistentVolumeClaim/vizier-data",
		"Service/vizier-service",
		"ServiceAccount/vizier",
	}
	tests := []struct {
		name        string
		overrides   map[string]any
		valuesFiles []string
		want        []string
	}{
		{
			name: "default values",
			want: withPVC,
		},
		{
			name:        "kind overlay",
			valuesFiles: []string{kindValues},
			want:        withPVC,
		},
		{
			name: "postgres with network policy",
			overrides: chartutil.CoalesceTables(postgresOverrides(), map[string]any{
				"networkPolicy": map[string]any{"enabled": true},
			}),
			want: []string{
				"Deployment/vizier",
				"NetworkPolicy/vizier",
				"Service/vizier-service",
				"ServiceAccount/vizier",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			docs := mustRender(t, tt.overrides, tt.valuesFiles...)
			if diff := cmp.Diff(tt.want, kinds(docs)); diff != "" {
				t.Errorf("rendered resources mismatch (-want +got):\n%s", diff)
			}
			for _, doc := range docs {
				if got := lookup(doc, "metadata", "namespace"); got != namespace {
					t.Errorf("%v/%v namespace = %v, want %q", doc["kind"], lookup(doc, "metadata", "name"), got, namespace)
				}
			}
		})
	}
}

func TestVizierChart_ServiceExposesGRPCPort(t *testing.T) {
	t.Parallel()

	selector := map[string]any{"app.kubernetes.io/name": "vizier", "app.kubernetes.io/instance": "vizier"}
	ports := []any{map[string]any{"name": "grpc", "port": 7077, "targetPort": "grpc", "protocol": "TCP"}}
	tests := []struct {
		name        string
		valuesFiles []string
		wantType    string
	}{
		{name: "default values", wantType: "ClusterIP"},
		{name: "kind overlay", valuesFiles: []string{kindValues}, wantType: "NodePort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			docs := mustRender(t, nil, tt.valuesFiles...)
			svc := mustFindDoc(t, docs, "Service", "vizier-service")
			want := map[string]any{"type": tt.wantType, "ports": ports, "selector": selector}
			got := map[string]any{"type": lookup(svc, "spec", "type"), "ports": lookup(svc, "spec", "ports"), "selector": lookup(svc, "spec", "selector")}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("vizier-service mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVizierChart_PodLabelsOmitChartVersion(t *testing.T) {
	t.Parallel()

	ch, _ := loadChart(t)
	docs := mustRender(t, nil)
	got := lookup(mustFindDoc(t, docs, "Deployment", "vizier"), "spec", "template", "metadata", "labels")
	want := map[string]any{
		"app.kubernetes.io/name":     "vizier",
		"app.kubernetes.io/instance": "vizier",
		"app.kubernetes.io/version":  ch.Metadata.AppVersion,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("pod labels mismatch (-want +got):\n%s", diff)
	}
}

func TestVizierChart_ContainerServesGRPCSecurely(t *testing.T) {
	t.Parallel()

	docs := mustRender(t, nil)
	container := vizierContainer(t, docs)
	tcpProbe := map[string]any{"port": "grpc"}
	want := map[string]any{
		"ports":          []any{map[string]any{"name": "grpc", "containerPort": 7077, "protocol": "TCP"}},
		"readinessProbe": tcpProbe,
		"livenessProbe":  tcpProbe,
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
			"capabilities":             map[string]any{"drop": []any{"ALL"}},
		},
	}
	got := map[string]any{
		"ports":           container["ports"],
		"readinessProbe":  lookup(container, "readinessProbe", "tcpSocket"),
		"livenessProbe":   lookup(container, "livenessProbe", "tcpSocket"),
		"securityContext": container["securityContext"],
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("vizier container mismatch (-want +got):\n%s", diff)
	}
	podSecurity := lookup(mustFindDoc(t, docs, "Deployment", "vizier"), "spec", "template", "spec", "securityContext")
	if nonRoot, _ := lookup(podSecurity, "runAsNonRoot").(bool); !nonRoot {
		t.Errorf("pod securityContext.runAsNonRoot = %v, want true", lookup(podSecurity, "runAsNonRoot"))
	}
}

func TestVizierChart_ImageDefaultsToAppVersion(t *testing.T) {
	t.Parallel()

	ch, _ := loadChart(t)
	wantImage := "xmc-vizier:" + ch.Metadata.AppVersion
	for _, valuesFiles := range [][]string{nil, {kindValues}} {
		container := vizierContainer(t, mustRender(t, nil, valuesFiles...))
		if got := container["image"]; got != wantImage {
			t.Errorf("values %v: image = %v, want %q", valuesFiles, got, wantImage)
		}
		if got, want := container["imagePullPolicy"], "IfNotPresent"; got != want {
			t.Errorf("values %v: imagePullPolicy = %v, want %q so that kind-loaded images are used", valuesFiles, got, want)
		}
	}
}

func TestVizierChart_ReflectionIsOnOnlyInKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		valuesFiles []string
		want        string
	}{
		{name: "default values", want: "false"},
		{name: "kind overlay", valuesFiles: []string{kindValues}, want: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := containerEnv(t, vizierContainer(t, mustRender(t, nil, tt.valuesFiles...)))
			if diff := cmp.Diff(value(tt.want), env["VIZIER_ENABLE_REFLECTION"]); diff != "" {
				t.Errorf("VIZIER_ENABLE_REFLECTION mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVizierChart_SQLiteStoresOnClaim(t *testing.T) {
	t.Parallel()

	docs := mustRender(t, nil)
	env := containerEnv(t, vizierContainer(t, docs))
	want := map[string]any{
		"VIZIER_PORT":              value("7077"),
		"VIZIER_ENABLE_REFLECTION": value("false"),
		"VIZIER_DATABASE_URL":      value("sqlite:////data/vizier.db"),
	}
	if diff := cmp.Diff(want, env); diff != "" {
		t.Errorf("sqlite env mismatch (-want +got):\n%s", diff)
	}
	volumes := asSlice(t, mustFindDoc(t, docs, "Deployment", "vizier"), "spec", "template", "spec", "volumes")
	dataVolume := map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "vizier-data"}}
	if !slices.ContainsFunc(volumes, func(v any) bool { return cmp.Equal(v, dataVolume) }) {
		t.Errorf("Deployment volumes = %v, want an entry %v", volumes, dataVolume)
	}
	tmpVolume := map[string]any{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "256Mi"}}
	if !slices.ContainsFunc(volumes, func(v any) bool { return cmp.Equal(v, tmpVolume) }) {
		t.Errorf("Deployment volumes = %v, want a bounded entry %v", volumes, tmpVolume)
	}
}

func TestVizierChart_PostgresReadsPasswordFromSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		passwordSecret map[string]any
		wantSecret     map[string]any
	}{
		{
			name:       "XMC postgres-secret by default",
			wantSecret: map[string]any{"name": "postgres-secret", "key": "postgres-password"},
		},
		{
			name:           "custom secret",
			passwordSecret: map[string]any{"name": "vizier-db", "key": "password"},
			wantSecret:     map[string]any{"name": "vizier-db", "key": "password"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			overrides := postgresOverrides()
			if tt.passwordSecret != nil {
				postgres, _ := lookup(overrides, "database", "postgres").(map[string]any)
				postgres["passwordSecret"] = tt.passwordSecret
			}
			env := containerEnv(t, vizierContainer(t, mustRender(t, overrides)))
			want := map[string]any{
				"VIZIER_PORT":              value("7077"),
				"VIZIER_ENABLE_REFLECTION": value("false"),
				"VIZIER_POSTGRES_HOST":     value("10.0.0.5"),
				"VIZIER_POSTGRES_PORT":     value("5432"),
				"VIZIER_POSTGRES_USER":     value("xmc"),
				"VIZIER_POSTGRES_DATABASE": value("vizier"),
				"VIZIER_POSTGRES_SSLMODE":  value("verify-full"),
				"VIZIER_POSTGRES_PASSWORD": map[string]any{"valueFrom": map[string]any{"secretKeyRef": tt.wantSecret}},
			}
			if diff := cmp.Diff(want, env); diff != "" {
				t.Errorf("postgres env mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVizierChart_NetworkPolicyAdmitsBridgeAndClients(t *testing.T) {
	t.Parallel()

	docs := mustRender(t, map[string]any{"networkPolicy": map[string]any{"enabled": true}})
	np := mustFindDoc(t, docs, "NetworkPolicy", "vizier")
	want := map[string]any{
		"podSelector": map[string]any{
			"matchLabels": map[string]any{"app.kubernetes.io/name": "vizier", "app.kubernetes.io/instance": "vizier"},
		},
		"policyTypes": []any{"Ingress"},
		"ingress": []any{map[string]any{
			"from": []any{
				map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "xmc-bridge"}}},
				map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"xmc.google.com/vizier-client": "true"}}},
			},
			"ports": []any{map[string]any{"port": "grpc", "protocol": "TCP"}},
		}},
	}
	if diff := cmp.Diff(want, np["spec"]); diff != "" {
		t.Errorf("NetworkPolicy spec mismatch (-want +got):\n%s", diff)
	}
}

func TestVizierChart_RejectsInvalidDatabaseValues(t *testing.T) {
	t.Parallel()

	postgresWith := func(postgres map[string]any) map[string]any {
		return map[string]any{"database": map[string]any{"type": "postgres", "postgres": postgres}}
	}
	tests := []struct {
		name      string
		overrides map[string]any
		wantErr   string
	}{
		{
			name:      "unknown type",
			overrides: map[string]any{"database": map[string]any{"type": "mysql"}},
			wantErr:   `database.type must be "sqlite" or "postgres", got "mysql"`,
		},
		{
			name:      "postgres without host",
			overrides: postgresWith(map[string]any{"user": "xmc"}),
			wantErr:   "database.postgres.host is required",
		},
		{
			name:      "postgres without user",
			overrides: postgresWith(map[string]any{"host": "10.0.0.5"}),
			wantErr:   "database.postgres.user is required",
		},
		{
			name: "postgres without password secret name",
			overrides: postgresWith(map[string]any{
				"host":           "10.0.0.5",
				"user":           "xmc",
				"passwordSecret": map[string]any{"name": ""},
			}),
			wantErr: "database.postgres.passwordSecret.name is required",
		},
		{
			name: "postgres without password secret key",
			overrides: postgresWith(map[string]any{
				"host":           "10.0.0.5",
				"user":           "xmc",
				"passwordSecret": map[string]any{"key": ""},
			}),
			wantErr: "database.postgres.passwordSecret.key is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := render(t, tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("render(%v) error = %v, want error containing %q", tt.overrides, err, tt.wantErr)
			}
		})
	}
}
