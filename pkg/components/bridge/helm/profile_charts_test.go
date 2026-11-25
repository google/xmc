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

package helm

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"xmanager_cloud/internal/logging"
)

// testProfileCharts returns the embedded default profile plus a "stable-test" profile whose ingestion-gateway chart is
// named chartName.
func testProfileCharts(chartName string) ProfileCharts {
	return ProfileCharts{
		DefaultProfile: defaultProfileCharts[DefaultProfile],
		"stable-test": fstest.MapFS{
			"components/ingestion-gateway/Chart.yaml": {Data: []byte("apiVersion: v2\nname: " + chartName + "\nversion: 0.1.0\n")},
		},
	}
}

func TestProfileChartsResolve(t *testing.T) {
	charts := testProfileCharts("ingestion-gateway-test")
	tests := []struct {
		name     string
		profile  string
		want     string
		wantWarn bool
	}{
		{name: "missing label", profile: "", want: DefaultProfile},
		{name: "default profile", profile: DefaultProfile, want: DefaultProfile},
		{name: "registered profile", profile: "stable-test", want: "stable-test"},
		{name: "unknown profile", profile: "stable-2099z", want: DefaultProfile, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			if got := charts.resolve(ctx, tc.profile); got != tc.want {
				t.Errorf("resolve(%q) = %q, want %q", tc.profile, got, tc.want)
			}
			if got := charts.lookup(tc.profile); got != tc.want {
				t.Errorf("lookup(%q) = %q, want %q", tc.profile, got, tc.want)
			}
			if gotWarn := strings.Contains(buf.String(), "label="+tc.profile); gotWarn != tc.wantWarn {
				t.Errorf("resolve(%q) logged WARN with the raw label = %v, want %v (logs: %q)", tc.profile, gotWarn, tc.wantWarn, buf.String())
			}
		})
	}
}

func TestProfileChartsComponentChart(t *testing.T) {
	charts := testProfileCharts("ingestion-gateway-test")
	charts["stable-metadata-only"] = fstest.MapFS{}
	tests := []struct {
		name         string
		profile      string
		component    string
		chartVersion string
		wantChart    string // empty means no embedded chart
		wantErr      bool
	}{
		{name: "default profile embeds ingestion-gateway", profile: DefaultProfile, component: "ingestion-gateway", wantChart: "ingestion-gateway"},
		{name: "registered profile embeds its own chart", profile: "stable-test", component: "ingestion-gateway", wantChart: "ingestion-gateway-test"},
		{name: "metadata-only entry falls back to the default profile", profile: "stable-metadata-only", component: "ingestion-gateway", wantChart: "ingestion-gateway"},
		{name: "fallback with matching chartVersion", profile: "stable-metadata-only", component: "ingestion-gateway", chartVersion: "0.1.0", wantChart: "ingestion-gateway"},
		{name: "fallback with mismatched chartVersion fails", profile: "stable-metadata-only", component: "ingestion-gateway", chartVersion: "0.2.0", wantErr: true},
		{name: "own chart ignores chartVersion", profile: "stable-test", component: "ingestion-gateway", chartVersion: "9.9.9", wantChart: "ingestion-gateway-test"},
		{name: "default profile ignores chartVersion", profile: DefaultProfile, component: "ingestion-gateway", chartVersion: "9.9.9", wantChart: "ingestion-gateway"},
		{name: "component without embedded chart", profile: DefaultProfile, component: "ray-operator"},
		{name: "component without chart in either profile", profile: "stable-metadata-only", component: "ray-operator"},
		{name: "path traversal component", profile: DefaultProfile, component: "../ingestion-gateway"},
		{name: "empty component", profile: DefaultProfile, component: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := charts.componentChart(tc.profile, tc.component, tc.chartVersion)
			if (err != nil) != tc.wantErr {
				t.Fatalf("componentChart(%q, %q, %q) error = %v, wantErr %v", tc.profile, tc.component, tc.chartVersion, err, tc.wantErr)
			}
			got := ""
			if ch != nil {
				got = ch.Name()
			}
			if got != tc.wantChart {
				t.Errorf("componentChart(%q, %q, %q) chart = %q, want %q", tc.profile, tc.component, tc.chartVersion, got, tc.wantChart)
			}
		})
	}
}

// TestDefaultProfileCharts checks the embedded registry: stable-2026b is known, embeds hpo, and its metadata-only
// ingestion-gateway entry resolves to the stable-2026a chart; stable-2026a does not ship hpo.
func TestDefaultProfileCharts(t *testing.T) {
	if got := defaultProfileCharts.resolve(context.Background(), "stable-2026b"); got != "stable-2026b" {
		t.Errorf("resolve(stable-2026b) = %q, want stable-2026b", got)
	}
	tests := []struct {
		profile   string
		component string
		wantChart string // empty means no embedded chart
	}{
		{profile: "stable-2026b", component: "ingestion-gateway", wantChart: "ingestion-gateway"},
		{profile: "stable-2026b", component: "hpo", wantChart: "hpo"},
		{profile: DefaultProfile, component: "hpo"},
	}
	for _, tc := range tests {
		ch, err := defaultProfileCharts.componentChart(tc.profile, tc.component, "0.1.0")
		got := ""
		if ch != nil {
			got = ch.Name()
		}
		if err != nil || got != tc.wantChart {
			t.Errorf("componentChart(%s, %s) = %q, %v; want chart %q", tc.profile, tc.component, got, err, tc.wantChart)
		}
	}
}

// TestDefaultProfileCharts_Stable2026bHPORendersWorkload renders the hpo chart that stable-2026b embeds as the
// xmc-cmp-hpo release: the workload renders and the Component CR does not.
func TestDefaultProfileCharts_Stable2026bHPORendersWorkload(t *testing.T) {
	ch, err := defaultProfileCharts.componentChart("stable-2026b", "hpo", "")
	if err != nil || ch == nil {
		t.Fatalf("componentChart(stable-2026b, hpo) = %v, %v; want the embedded hpo chart", ch, err)
	}
	releaseOpts := chartutil.ReleaseOptions{Name: "xmc-cmp-hpo", Namespace: "xmc", Revision: 1, IsInstall: true}
	vals, err := chartutil.ToRenderValues(ch, map[string]any{
		"hpoDeployment": map[string]any{
			"enabled":     true,
			"redisAddr":   "redis-service:6379",
			"essAddr":     "grpc-service:50051",
			"essAudience": "test-ess-audience",
		},
	}, releaseOpts, nil)
	if err != nil {
		t.Fatalf("ToRenderValues(xmc-cmp-hpo) failed: %v", err)
	}
	rendered, err := engine.Render(ch, vals)
	if err != nil {
		t.Fatalf("engine.Render(xmc-cmp-hpo) failed: %v", err)
	}
	if got := strings.TrimSpace(rendered["hpo/templates/component.yaml"]); got != "" {
		t.Errorf("xmc-cmp-hpo rendered component.yaml = %q, want empty (guarded by not hasPrefix xmc-cmp-)", got)
	}
	if got := rendered["hpo/templates/deployment.yaml"]; !strings.Contains(got, "name: xmc-study-controller") {
		t.Errorf("xmc-cmp-hpo rendered deployment.yaml = %q, want the xmc-study-controller Deployment", got)
	}
}

func TestLoadEmbeddedChart(t *testing.T) {
	tests := []struct {
		name      string
		fsys      fs.FS
		wantChart string
		wantErr   bool
	}{
		{name: "nil filesystem", fsys: nil},
		{name: "missing root", fsys: fstest.MapFS{}},
		{
			name: "chart with templates",
			fsys: fstest.MapFS{
				"components/demo/Chart.yaml":            {Data: []byte("apiVersion: v2\nname: demo\nversion: 1.0.0\n")},
				"components/demo/templates/config.yaml": {Data: []byte("kind: ConfigMap\n")},
			},
			wantChart: "demo",
		},
		{
			name:    "malformed Chart.yaml",
			fsys:    fstest.MapFS{"components/demo/Chart.yaml": {Data: []byte("name: [unterminated\n")}},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := loadEmbeddedChart(tc.fsys, "components/demo")
			if (err != nil) != tc.wantErr {
				t.Fatalf("loadEmbeddedChart() error = %v, wantErr %v", err, tc.wantErr)
			}
			got := ""
			if ch != nil {
				got = ch.Name()
			}
			if got != tc.wantChart {
				t.Errorf("loadEmbeddedChart() chart = %q, want %q", got, tc.wantChart)
			}
		})
	}
}
