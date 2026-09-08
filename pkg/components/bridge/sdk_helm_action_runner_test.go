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
	"testing"

	"helm.sh/helm/v3/pkg/chart"
)

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
