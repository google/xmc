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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/storage/driver"
)

// newFakeAPIServerGetter returns a RESTClientGetter pointing at an in-process API server that only serves /version,
// which is sufficient for Helm's reachability check when using the in-memory storage driver.
func newFakeAPIServerGetter(t *testing.T) genericclioptions.RESTClientGetter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"30","gitVersion":"v1.30.0"}`))
	}))
	t.Cleanup(srv.Close)

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	content := "apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: " + srv.URL +
		"\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n"
	if err := os.WriteFile(kubeconfig, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &kubeconfig
	return flags
}

func TestSDKHelmActionRunner_Status(t *testing.T) {
	tests := []struct {
		name           string
		runner         func(t *testing.T) *SDKHelmActionRunner
		wantErr        bool
		wantNotFoundIs bool
	}{
		{
			name:    "nil runner",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return nil },
			wantErr: true,
		},
		{
			name:    "nil restClientGetter",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return NewSDKHelmActionRunner(nil) },
			wantErr: true,
		},
		{
			name: "missing release maps to driver.ErrReleaseNotFound",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newFakeAPIServerGetter(t), WithDriver("memory"))
			},
			wantErr:        true,
			wantNotFoundIs: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := tt.runner(t).Status(context.Background(), "xmc-cmp-ingestion-gateway", "xmc")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Status() error = %v, wantErr %v", err, tt.wantErr)
			}
			if rel != nil {
				t.Errorf("Status() release = %v, want nil", rel)
			}
			if got := errors.Is(err, driver.ErrReleaseNotFound); got != tt.wantNotFoundIs {
				t.Errorf("errors.Is(err, driver.ErrReleaseNotFound) = %v, want %v (err: %v)", got, tt.wantNotFoundIs, err)
			}
		})
	}
}

func TestSDKHelmActionRunner_Options(t *testing.T) {
	var nilRunner *SDKHelmActionRunner
	rel, err := nilRunner.Upgrade(context.Background(), "test", "xmc", nil, nil)
	if err == nil || rel != nil {
		t.Errorf("expected error on nil runner Upgrade, got rel=%v, err=%v", rel, err)
	}

	dummyChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "custom-chart", Version: "1.0.0"},
	}
	runner := NewSDKHelmActionRunner(nil,
		WithDriver("memory"),
		WithRunnerChartPath("/tmp/charts"),
		WithRunnerChartObj(dummyChart),
	)
	if runner.helmDriver != "memory" {
		t.Errorf("expected driver memory, got %q", runner.helmDriver)
	}
	if runner.chartPath != "/tmp/charts" {
		t.Errorf("expected chart path /tmp/charts, got %q", runner.chartPath)
	}
	if runner.chartObj != dummyChart {
		t.Errorf("expected custom chart object, got %v", runner.chartObj)
	}

	// Calling Upgrade with nil restClientGetter should fail at actionConfig.Init
	_, err = runner.Upgrade(context.Background(), "test", "xmc", nil, map[string]any{})
	if err == nil {
		t.Error("expected error from Upgrade when restClientGetter is nil")
	}

	// Calling Uninstall on nil runner returns error
	resp, err := nilRunner.Uninstall(context.Background(), "test", "xmc")
	if err == nil || resp != nil {
		t.Errorf("expected error on nil runner Uninstall, got resp=%v, err=%v", resp, err)
	}

	// Calling Uninstall with nil restClientGetter should fail at actionConfig.Init
	_, err = runner.Uninstall(context.Background(), "test", "xmc")
	if err == nil {
		t.Error("expected error from Uninstall when restClientGetter is nil")
	}
}

func TestSDKHelmActionRunner_ResolveChart_RWMutex(t *testing.T) {
	runner := NewSDKHelmActionRunner(nil, WithRunnerChartPath("/invalid/path/to/chart"))

	// First load fails
	_, err := runner.resolveChart("test-release", nil)
	if err == nil {
		t.Fatal("expected error loading from invalid path, got nil")
	}

	// Dynamic override of chartObj should be thread-safe and succeed immediately
	expectedChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "test-chart"},
	}
	resolved, err := runner.resolveChart("test-release", expectedChart)
	if err != nil {
		t.Fatalf("unexpected error with explicit chart: %v", err)
	}
	if resolved.Name() != "test-chart" {
		t.Errorf("expected resolved chart name %q, got %q", "test-chart", resolved.Name())
	}
}

func TestSDKHelmActionRunner_Uninstall(t *testing.T) {
	var nilRunner *SDKHelmActionRunner
	rel, err := nilRunner.Uninstall(context.Background(), "test", "xmc")
	if err == nil || rel != nil {
		t.Errorf("expected error on nil runner Uninstall, got rel=%v, err=%v", rel, err)
	}

	runner := NewSDKHelmActionRunner(nil)
	_, err = runner.Uninstall(context.Background(), "test", "xmc")
	if err == nil {
		t.Error("expected error from Uninstall when restClientGetter is nil")
	}
}

func TestSDKHelmActionRunner_ResolveChart_DefaultFallback(t *testing.T) {
	runner := NewSDKHelmActionRunner(nil)
	resolved, err := runner.resolveChart("xmc-cmp-ray-operator", nil)
	if err != nil {
		t.Fatalf("unexpected error resolving default chart: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected non-nil default chart")
	}
	if resolved.Name() != "xmc-cmp-ray-operator" {
		t.Errorf("expected chart name %q, got %q", "xmc-cmp-ray-operator", resolved.Name())
	}
	if resolved.Metadata == nil || resolved.Metadata.APIVersion != "v2" {
		t.Errorf("expected v2 metadata, got %v", resolved.Metadata)
	}
}
