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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"xmanager_cloud/pkg/components/crreader"
)

func baselineClickHouseCR() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata":   map[string]any{"name": "clickhouse", "namespace": "xmc"},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"parameters": map[string]any{
					"host":              "ch-spec.xmc-metrics.svc.cluster.local",
					"clusterEndpoints":  "ch-spec-0.xmc-metrics.svc.cluster.local:9000,ch-spec-1.xmc-metrics.svc.cluster.local:9000",
					"nativePort":        "9000",
					"defaultDatabase":   "analytics",
					"user":              "ch_reader",
					"secretName":        "clickhouse-secret",
					"secretPasswordKey": "clickhouse-password",
				},
			},
			"status": map[string]any{
				"state": "COMPONENT_STATE_ENABLED",
				"access": map[string]any{
					"envVars": map[string]any{
						"HOST":        "ch-live.xmc-metrics.svc.cluster.local",
						"PORT":        "9440",
						"SECRET_NAME": "clickhouse-live-secret",
					},
				},
			},
		},
	}
}

func modifyCR(mutator func(u *unstructured.Unstructured)) *crreader.FakeGetter {
	u := baselineClickHouseCR()
	mutator(u)
	return &crreader.FakeGetter{Component: u}
}

func crSource(mutator func(u *unstructured.Unstructured)) Source {
	return Source{Enabled: true, Getter: modifyCR(mutator), Secrets: baselineSecrets()}
}

func baselineSecrets() *crreader.FakeSecretReader {
	return &crreader.FakeSecretReader{
		Secrets: map[string]map[string]string{
			"xmc/clickhouse-live-secret": {"clickhouse-password": "live-secret-pw\n"},
			"xmc/clickhouse-secret":      {"clickhouse-password": "spec-secret-pw"},
		},
	}
}

type optionsSummary struct {
	Addr        []string
	Auth        clickhouse.Auth
	DialTimeout time.Duration
	Compression clickhouse.CompressionMethod
}

func summarizeOptions(opts *clickhouse.Options) optionsSummary {
	var compMethod clickhouse.CompressionMethod
	if opts.Compression != nil {
		compMethod = opts.Compression.Method
	}
	return optionsSummary{Addr: opts.Addr, Auth: opts.Auth, DialTimeout: opts.DialTimeout, Compression: compMethod}
}

func TestResolveOptions(t *testing.T) {
	baseEnvSummary := optionsSummary{
		Addr:        []string{"env-ch.local:9000"},
		Auth:        clickhouse.Auth{Database: "env_db", Username: "env_user", Password: "env_password"},
		DialTimeout: 5 * time.Second,
	}
	crSummary := func(addrs []string, db, user, pw string) optionsSummary {
		return optionsSummary{Addr: addrs, Auth: clickhouse.Auth{Database: db, Username: user, Password: pw}, DialTimeout: 10 * time.Second, Compression: clickhouse.CompressionLZ4}
	}
	baseCRSummary := crSummary([]string{"ch-live.xmc-metrics.svc.cluster.local:9440"}, "analytics", "ch_reader", "live-secret-pw")

	tests := []struct {
		name    string
		setup   func(t *testing.T)
		src     Source
		want    optionsSummary
		wantErr bool
	}{
		{name: "disabled source uses static env options", src: Source{Enabled: false}, want: baseEnvSummary},
		{name: "enabled source with nil getter returns hard error", src: Source{Enabled: true}, wantErr: true},
		{
			name: "NotFound CR falls back to static env options",
			src:  Source{Enabled: true, Getter: &crreader.FakeGetter{Err: apierrors.NewNotFound(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "clickhouse")}},
			want: baseEnvSummary,
		},
		{name: "non-NotFound API error returns hard error", src: Source{Enabled: true, Getter: &crreader.FakeGetter{Err: errors.New("forbidden")}}, wantErr: true},
		{
			name: "spec.enabled=false CR falls back to static env options",
			src: crSource(func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, false, "spec", "enabled")
			}),
			want: baseEnvSummary,
		},
		{
			name: "status.state=COMPONENT_STATE_DISABLED falls back to static env options",
			src: crSource(func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, "COMPONENT_STATE_DISABLED", "status", "state")
			}),
			want: baseEnvSummary,
		},
		{name: "resolves from status.access.envVars HOST, PORT, and SECRET_NAME", src: crSource(func(*unstructured.Unstructured) {}), want: baseCRSummary},
		{
			name: "resolves from status.access.envVars ENDPOINT when HOST is absent",
			src: crSource(func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedMap(u.Object, map[string]any{"ENDPOINT": "ch-ep-0.local, ch-ep-1.local:9009", "PORT": "9000"}, "status", "access", "envVars")
			}),
			want: crSummary([]string{"ch-ep-0.local:9000", "ch-ep-1.local:9009"}, "analytics", "ch_reader", "spec-secret-pw"),
		},
		{
			name: "resolves from spec.parameters clusterEndpoints when status.access.envVars is absent",
			src:  crSource(func(u *unstructured.Unstructured) { unstructured.RemoveNestedField(u.Object, "status", "access") }),
			want: crSummary([]string{"ch-spec-0.xmc-metrics.svc.cluster.local:9000", "ch-spec-1.xmc-metrics.svc.cluster.local:9000"}, "analytics", "ch_reader", "spec-secret-pw"),
		},
		{
			name: "resolves from spec.parameters host and defaults when clusterEndpoints is empty",
			src: crSource(func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "status", "access")
				_ = unstructured.SetNestedMap(u.Object, map[string]any{"host": "ch-only-host.local"}, "spec", "parameters")
			}),
			want: crSummary([]string{"ch-only-host.local:9000"}, "default", "default", "spec-secret-pw"),
		},
		{
			name: "CSI CLICKHOUSE_PASSWORD_FILE takes precedence over SecretReader",
			setup: func(t *testing.T) {
				pwFile := filepath.Join(t.TempDir(), "ch-pw")
				if err := os.WriteFile(pwFile, []byte(" csi-pw \r\n"), 0600); err != nil {
					t.Fatalf("failed to write password file: %v", err)
				}
				t.Setenv("CLICKHOUSE_PASSWORD_FILE", pwFile)
			},
			src:  Source{Enabled: true, Getter: &crreader.FakeGetter{Component: baselineClickHouseCR()}},
			want: crSummary([]string{"ch-live.xmc-metrics.svc.cluster.local:9440"}, "analytics", "ch_reader", " csi-pw "),
		},
		{
			name:    "unreadable CSI CLICKHOUSE_PASSWORD_FILE returns hard error",
			setup:   func(t *testing.T) { t.Setenv("CLICKHOUSE_PASSWORD_FILE", filepath.Join(t.TempDir(), "nonexistent")) },
			src:     crSource(func(*unstructured.Unstructured) {}),
			wantErr: true,
		},
		{
			name:    "nil SecretReader without CSI returns hard error",
			src:     Source{Enabled: true, Getter: &crreader.FakeGetter{Component: baselineClickHouseCR()}},
			wantErr: true,
		},
		{
			name:    "missing Secret or key returns hard error",
			src:     Source{Enabled: true, Getter: &crreader.FakeGetter{Component: baselineClickHouseCR()}, Secrets: &crreader.FakeSecretReader{}},
			wantErr: true,
		},
		{
			name: "malformed spec.parameters returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				u.Object["spec"] = map[string]any{"enabled": true, "parameters": "bad"}
			}),
			wantErr: true,
		},
		{
			name: "malformed status.access.envVars returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				u.Object["status"] = map[string]any{"state": "COMPONENT_STATE_ENABLED", "access": map[string]any{"envVars": "bad"}}
			}),
			wantErr: true,
		},
		{
			name: "invalid port in status.access.envVars returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, "70000", "status", "access", "envVars", "PORT")
			}),
			wantErr: true,
		},
		{
			name: "invalid endpoint in clusterEndpoints returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "status", "access")
				_ = unstructured.SetNestedField(u.Object, ":9000", "spec", "parameters", "clusterEndpoints")
			}),
			wantErr: true,
		},
		{
			name: "missing host and endpoints returns hard error",
			src: crSource(func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "status", "access")
				_ = unstructured.SetNestedMap(u.Object, map[string]any{}, "spec", "parameters")
			}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLICKHOUSE_DSN", "")
			t.Setenv("CLICKHOUSE_HOST", "env-ch.local")
			t.Setenv("CLICKHOUSE_PORT", "9000")
			t.Setenv("CLICKHOUSE_DATABASE", "env_db")
			t.Setenv("CLICKHOUSE_USER", "env_user")
			t.Setenv("CLICKHOUSE_PASSWORD", "env_password")
			t.Setenv("CLICKHOUSE_PASSWORD_FILE", "")
			if tt.setup != nil {
				tt.setup(t)
			}
			got, err := ResolveOptions(context.Background(), tt.src)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, summarizeOptions(got)); diff != "" {
					t.Errorf("ResolveOptions() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestParseEndpointsEdgeCases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, port string }{
		{" , , ", "9000"},
		{"ch.local", "invalid"},
		{"ch.local:bad:port", "9000"},
	} {
		if _, err := parseEndpoints(tc.raw, tc.port); err == nil {
			t.Errorf("parseEndpoints(%q, %q) succeeded, want error", tc.raw, tc.port)
		}
	}
	if _, err := joinValidatedHostPort("   ", "9000"); err == nil {
		t.Errorf("joinValidatedHostPort(empty host) succeeded, want error")
	}
}

func TestOptionsFingerprint(t *testing.T) {
	t.Parallel()
	if got := OptionsFingerprint(nil); got != "" {
		t.Errorf("OptionsFingerprint(nil) = %q, want empty string", got)
	}
	base := &clickhouse.Options{
		Addr: []string{"ch-0.local:9000", "ch-1.local:9000"},
		Auth: clickhouse.Auth{Database: "analytics", Username: "reader", Password: "secret"},
	}
	baseFP := OptionsFingerprint(base)
	if baseFP == "" {
		t.Fatal("OptionsFingerprint(base) returned empty string")
	}
	if got := OptionsFingerprint(base); got != baseFP {
		t.Errorf("OptionsFingerprint(base) = %q, want deterministic %q", got, baseFP)
	}
	for _, variant := range []*clickhouse.Options{
		{Addr: []string{"ch-0.local:9000"}, Auth: base.Auth},
		{Addr: base.Addr, Auth: clickhouse.Auth{Database: "other", Username: "reader", Password: "secret"}},
		{Addr: base.Addr, Auth: clickhouse.Auth{Database: "analytics", Username: "other", Password: "secret"}},
		{Addr: base.Addr, Auth: clickhouse.Auth{Database: "analytics", Username: "reader", Password: "other"}},
	} {
		if got := OptionsFingerprint(variant); got == baseFP {
			t.Errorf("OptionsFingerprint(%+v) = %q, want distinct fingerprint from base", variant, got)
		}
	}
}
