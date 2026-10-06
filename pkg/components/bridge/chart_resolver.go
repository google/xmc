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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"

	"xmanager_cloud/internal/logging"
)

// ChartPathResolver resolves the filesystem Helm chart path for a given component reference.
type ChartPathResolver func(ref ComponentRef) string

// ResolveComponentChartPath resolves the filesystem chart path for a specific 1P component.
// Resolution precedence:
// 1. Explicit component override path (overridePath), if non-empty and within baseChartPath (when baseChartPath is set).
// 2. Per-component directory or archive under baseChartPath:
//   - <baseChartPath>/components/<componentName> (containing Chart.yaml)
//   - <baseChartPath>/<componentName> (containing Chart.yaml)
//   - <baseChartPath>/<componentName>.tgz
//
// 3. Fallback to baseChartPath (umbrella chart directory or archive).
func ResolveComponentChartPath(baseChartPath string, ref ComponentRef, overridePath string) string {
	if resolvedOverride, ok := resolveOverrideChartPath(baseChartPath, overridePath); ok {
		return resolvedOverride
	}
	if baseChartPath == "" || !isSafeComponentSegment(ref.Name) {
		return baseChartPath
	}
	dirCandidates := []string{
		filepath.Join(baseChartPath, "components", ref.Name),
		filepath.Join(baseChartPath, ref.Name),
	}
	for _, dir := range dirCandidates {
		if chartFileExists(filepath.Join(dir, "Chart.yaml")) {
			return dir
		}
	}
	tgzCandidate := filepath.Join(baseChartPath, ref.Name+".tgz")
	if chartFileExists(tgzCandidate) {
		return tgzCandidate
	}
	return baseChartPath
}

func resolveOverrideChartPath(baseChartPath, overridePath string) (string, bool) {
	if overridePath == "" {
		return "", false
	}
	if containsPathTraversal(overridePath) {
		logging.Error(context.Background(), "Rejecting component chartPath override containing path traversal segments", "overridePath", overridePath)
		return "", false
	}
	if baseChartPath == "" {
		return filepath.Clean(overridePath), true
	}
	candidate := filepath.Clean(overridePath)
	if !filepath.IsAbs(overridePath) {
		candidate = filepath.Join(baseChartPath, overridePath)
	}
	if !isWithinBaseDir(baseChartPath, candidate) {
		logging.Error(context.Background(), "Rejecting component chartPath override outside baseChartPath", "overridePath", overridePath, "baseChartPath", baseChartPath)
		return "", false
	}
	return candidate, true
}

func containsPathTraversal(p string) bool {
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(p), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func isSafeComponentSegment(name string) bool {
	return name != "" && !containsPathTraversal(name) && !strings.ContainsAny(name, `/\`)
}

func isWithinBaseDir(baseDir, candidatePath string) bool {
	rel, err := filepath.Rel(filepath.Clean(baseDir), filepath.Clean(candidatePath))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func chartFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func chartExistsOnDisk(chartPath string) bool {
	if chartPath == "" {
		return false
	}
	return chartFileExists(filepath.Join(chartPath, "Chart.yaml")) || chartFileExists(chartPath)
}

func (m *K8sHelmManager) resolveChartPathForComponent(ref ComponentRef) string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	savedOverride := m.componentChartPaths[componentKey(ref.Namespace, ref.Name)]
	resolver := m.chartPathResolver
	basePath := m.chartPath
	m.mu.RUnlock()

	if savedOverride == "" && resolver != nil {
		savedOverride = resolver(ref)
	}
	return ResolveComponentChartPath(basePath, ref, savedOverride)
}

func (m *K8sHelmManager) loadComponentChart(chartPath string) (*chart.Chart, error) {
	if m == nil || chartPath == "" || chartPath == m.chartPath {
		return nil, nil
	}
	if !chartExistsOnDisk(chartPath) {
		logging.Warn(context.Background(), "Resolved component chart path does not exist on disk; falling back to default runner chart", "chartPath", chartPath)
		return nil, nil
	}
	m.mu.RLock()
	cached := m.chartCache[chartPath]
	m.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	loaded, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load component Helm chart from %q: %w", chartPath, err)
	}
	m.mu.Lock()
	if m.chartCache == nil {
		m.chartCache = make(map[string]*chart.Chart)
	}
	m.chartCache[chartPath] = loaded
	m.mu.Unlock()
	return loaded, nil
}
