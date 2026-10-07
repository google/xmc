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
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestResolveComponentChartPath(t *testing.T) {
	baseDir := t.TempDir()
	compSubDir := filepath.Join(baseDir, "components", "ingestion-gateway")
	if err := os.MkdirAll(compSubDir, 0755); err != nil {
		t.Fatalf("failed to create component subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(compSubDir, "Chart.yaml"), []byte("apiVersion: v2\nname: ingestion-gateway\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	directSubDir := filepath.Join(baseDir, "rod-data-provider")
	if err := os.MkdirAll(directSubDir, 0755); err != nil {
		t.Fatalf("failed to create direct subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directSubDir, "Chart.yaml"), []byte("apiVersion: v2\nname: rod-data-provider\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	tgzPath := filepath.Join(baseDir, "health-service.tgz")
	if err := os.WriteFile(tgzPath, []byte("dummy-tgz"), 0644); err != nil {
		t.Fatalf("failed to write tgz file: %v", err)
	}

	tests := []struct {
		name         string
		basePath     string
		ref          ComponentRef
		overridePath string
		wantPath     string
	}{
		{
			name:         "ExplicitRelativeOverrideWithinBaseDir",
			basePath:     baseDir,
			ref:          ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			overridePath: "custom/override/chart",
			wantPath:     filepath.Join(baseDir, "custom", "override", "chart"),
		},
		{
			name:         "ExplicitOverrideWithoutBaseDir",
			basePath:     "",
			ref:          ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			overridePath: "/custom/override/chart",
			wantPath:     "/custom/override/chart",
		},
		{
			name:         "RejectOutOfBoundsOverrideFallsBackToBaseDirDiscovery",
			basePath:     baseDir,
			ref:          ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			overridePath: "/outside/base/dir/chart",
			wantPath:     compSubDir,
		},
		{
			name:         "RejectPathTraversalInOverride",
			basePath:     baseDir,
			ref:          ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			overridePath: "../../etc/passwd",
			wantPath:     compSubDir,
		},
		{
			name:     "RejectPathTraversalInComponentName",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "../escape"},
			wantPath: baseDir,
		},
		{
			name:     "ComponentsSubdirectoryResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"},
			wantPath: compSubDir,
		},
		{
			name:     "DirectSubdirectoryResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "rod-data-provider"},
			wantPath: directSubDir,
		},
		{
			name:     "PackagedArchiveResolution",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "health-service"},
			wantPath: tgzPath,
		},
		{
			name:     "UmbrellaChartFallback",
			basePath: baseDir,
			ref:      ComponentRef{Namespace: "xmc", Name: "unknown-component"},
			wantPath: baseDir,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveComponentChartPath(tc.basePath, tc.ref, tc.overridePath)
			if got != tc.wantPath {
				t.Errorf("ResolveComponentChartPath() = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestK8sHelmManager_PerComponentChartLoading(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	compChartDir := filepath.Join(baseDir, "components", "ingestion-gateway")
	if err := os.MkdirAll(compChartDir, 0755); err != nil {
		t.Fatalf("failed to create component chart dir: %v", err)
	}
	chartYAML := "apiVersion: v2\nname: ingestion-gateway-chart\nversion: 1.2.3\n"
	if err := os.WriteFile(filepath.Join(compChartDir, "Chart.yaml"), []byte(chartYAML), 0644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(
		client,
		"xmc",
		"xmanager-cloud",
		WithActionRunner(runner),
		WithChartPath(baseDir),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	loadedChart := runner.lastChart
	runner.mu.Unlock()

	if loadedChart == nil || loadedChart.Metadata == nil || loadedChart.Metadata.Name != "ingestion-gateway-chart" {
		t.Fatalf("expected per-component chart 'ingestion-gateway-chart' to be loaded and passed to runner, got %+v", loadedChart)
	}
}
