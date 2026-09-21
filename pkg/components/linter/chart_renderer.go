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
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"gopkg.in/yaml.v3"
)

var chartContextCache sync.Map

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

// RenderChartTemplate renders a Helm template against Chart.yaml and values.yaml in chartRoot.
func RenderChartTemplate(chartRoot, templatePath string, rawData []byte) ([]byte, error) {
	data, err := loadChartTemplateContext(chartRoot)
	if err != nil {
		return nil, err
	}
	tmpl := template.New(filepath.Base(templatePath))
	tmpl.Funcs(chartTemplateFuncs(tmpl))
	helpers, _ := filepath.Glob(filepath.Join(chartRoot, "templates", "_*"))
	for _, h := range helpers {
		if info, err := os.Stat(h); err == nil && !info.IsDir() {
			if _, err := tmpl.ParseFiles(h); err != nil {
				return nil, fmt.Errorf("failed to parse helper %s: %w", h, err)
			}
		}
	}
	var buf bytes.Buffer
	if _, err := tmpl.Parse(string(rawData)); err != nil {
		return nil, fmt.Errorf("failed to parse template %s: %w", templatePath, err)
	}
	err = tmpl.Execute(&buf, data)
	return buf.Bytes(), err
}

func loadChartTemplateContext(chartRoot string) (map[string]any, error) {
	if cached, ok := chartContextCache.Load(chartRoot); ok {
		return cached.(map[string]any), nil
	}
	chartMeta, err := readYAMLFileMap(chartRoot, "Chart.yaml", "Chart.yml")
	if err != nil {
		return nil, fmt.Errorf("failed to read Chart.yaml in %s: %w", chartRoot, err)
	}
	chartMeta["Name"] = "release-name"
	for src, dst := range map[string]string{"name": "Name", "version": "Version", "appVersion": "AppVersion", "description": "Description", "type": "Type", "apiVersion": "APIVersion"} {
		if s, ok := chartMeta[src].(string); ok && s != "" {
			chartMeta[dst] = s
		}
	}
	valuesMap, err := readYAMLFileMap(chartRoot, "values.yaml", "values.yml")
	if err != nil {
		return nil, fmt.Errorf("failed to read values.yaml in %s: %w", chartRoot, err)
	}
	data := map[string]any{"Chart": chartMeta, "Values": valuesMap, "Release": map[string]any{"Name": chartMeta["Name"], "Namespace": "xmc", "Service": "Helm", "IsInstall": true, "IsUpgrade": false}}
	chartContextCache.Store(chartRoot, data)
	return data, nil
}

func readYAMLFileMap(dir string, candidates ...string) (map[string]any, error) {
	for _, name := range candidates {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			out := make(map[string]any)
			if err := yaml.Unmarshal(b, &out); err != nil {
				return nil, err
			}
			return out, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return make(map[string]any), nil
}

func chartTemplateFuncs(tmpl *template.Template) template.FuncMap {
	toStr := func(v any) string {
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	indent := func(spaces int, v string) string {
		return strings.Repeat(" ", spaces) + strings.ReplaceAll(v, "\n", "\n"+strings.Repeat(" ", spaces))
	}
	return template.FuncMap{
		"default": func(d any, given ...any) any {
			if len(given) > 0 && given[0] != nil {
				if v := reflect.ValueOf(given[0]); !v.IsZero() && ((v.Kind() != reflect.Slice && v.Kind() != reflect.Map) || v.Len() > 0) {
					return given[0]
				}
			}
			return d
		},
		"quote":    func(v any) string { return strconv.Quote(toStr(v)) },
		"toString": toStr,
		"trim":     func(v any) string { return strings.TrimSpace(toStr(v)) },
		"lower":    func(v any) string { return strings.ToLower(toStr(v)) },
		"upper":    func(v any) string { return strings.ToUpper(toStr(v)) },
		"indent":   indent,
		"nindent":  func(spaces int, v string) string { return "\n" + indent(spaces, v) },
		"toYaml":   func(v any) string { b, _ := yaml.Marshal(v); return strings.TrimSuffix(string(b), "\n") },
		"include": func(name string, d any) (string, error) {
			var b bytes.Buffer
			err := tmpl.ExecuteTemplate(&b, name, d)
			return b.String(), err
		},
	}
}
