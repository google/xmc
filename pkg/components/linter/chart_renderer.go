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

package linter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

type cachedChart struct {
	chartName     string
	renderedFiles map[string]string
}

var (
	renderCacheMu sync.RWMutex
	renderCache   = make(map[string]*cachedChart)
)

// isChartOrRulesConfigFile returns true if the YAML file is a Helm chart metadata/values/helper file or a rules file.
func isChartOrRulesConfigFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "rules.yaml" || base == "rules.yml" {
		return true
	}
	chartRoot := findChartRoot(path)
	if chartRoot == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(chartRoot, absPath)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 1 && parts[0] == "templates" {
		return strings.HasPrefix(parts[len(parts)-1], "_")
	}
	return true
}

// findChartRoot walks up the directory tree from filePath to locate an enclosing Helm chart root (containing Chart.yaml or Chart.yml).
func findChartRoot(filePath string) string {
	absPath, _ := filepath.Abs(filePath)
	for dir := filepath.Dir(absPath); filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		for _, name := range []string{"Chart.yaml", "Chart.yml"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				return dir
			}
		}
	}
	return ""
}

// RenderChartTemplate loads the Helm chart at chartRoot (using a thread-safe cache) and renders templatePath
// against Chart.yaml, default values.yaml, and helper partials using the Helm v3 engine.
func RenderChartTemplate(chartRoot, templatePath string) ([]byte, error) {
	absRoot, err := filepath.Abs(chartRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for chart root %s: %w", chartRoot, err)
	}
	cc, err := getOrRenderChart(absRoot, templatePath)
	if err != nil {
		return nil, err
	}
	key, err := renderedTemplateKey(absRoot, cc.chartName, templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute rendered template key for %s: %w", templatePath, err)
	}
	rendered, ok := cc.renderedFiles[key]
	if !ok {
		return nil, fmt.Errorf("rendered output for %s (key %q) not found in chart %s", templatePath, key, absRoot)
	}
	return []byte(rendered), nil
}

func getOrRenderChart(absRoot, templatePath string) (*cachedChart, error) {
	renderCacheMu.RLock()
	cc, ok := renderCache[absRoot]
	renderCacheMu.RUnlock()
	if ok {
		return cc, nil
	}
	renderCacheMu.Lock()
	defer renderCacheMu.Unlock()
	if cc, ok = renderCache[absRoot]; ok {
		return cc, nil
	}
	cc, err := loadAndRenderChart(absRoot, templatePath)
	if err != nil {
		return nil, err
	}
	renderCache[absRoot] = cc
	return cc, nil
}

func loadAndRenderChart(absRoot, templatePath string) (*cachedChart, error) {
	ch, err := loader.LoadDir(absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart in %s: %w", absRoot, err)
	}
	releaseOpts := chartutil.ReleaseOptions{
		Name:      ch.Name(),
		Namespace: "xmc",
		Revision:  1,
		IsInstall: true,
	}
	renderValues, err := chartutil.ToRenderValues(ch, ch.Values, releaseOpts, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build Helm render values in %s: %w", absRoot, err)
	}
	renderedFiles, err := engine.Render(ch, renderValues)
	if err != nil {
		return nil, fmt.Errorf("failed to render Helm template %s: %w", templatePath, err)
	}
	return &cachedChart{chartName: ch.Name(), renderedFiles: renderedFiles}, nil
}

// renderedTemplateKey computes the "<chartName>/<relPath>" lookup key produced by Helm's engine.Render.
func renderedTemplateKey(chartRoot, chartName, templatePath string) (string, error) {
	absRoot, err := filepath.Abs(chartRoot)
	if err != nil {
		return "", err
	}
	absTmpl, err := filepath.Abs(templatePath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTmpl)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(chartName, rel)), nil
}
