// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package linter implements the 3-pass static validation engine for XMC component manifests.
package linter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RulesConfig represents the rules.yaml schema for xmc-linter.
type RulesConfig struct {
	Profile             string `yaml:"profile"`
	CompatibilityMatrix struct {
		ComponentDependencies []struct {
			Component string `yaml:"component"`
			Version   string `yaml:"version"`
			Requires  []struct {
				Component    string `yaml:"component"`
				VersionRange string `yaml:"versionRange"`
			} `yaml:"requires"`
		} `yaml:"componentDependencies"`
	} `yaml:"compatibilityMatrix"`
	StaticChecks struct {
		EnforceDeletionProtection bool `yaml:"enforceDeletionProtection"`
		MaxDrainTimeoutSeconds    int  `yaml:"maxDrainTimeoutSeconds"`
		EnforceNamespaceScope     bool `yaml:"enforceNamespaceScope"`
	} `yaml:"staticChecks"`
}

// ComponentManifest represents a parsed Component custom resource YAML file.
type ComponentManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Enabled            bool              `yaml:"enabled"`
		ComponentType      string            `yaml:"componentType"`
		ComponentClass     string            `yaml:"componentClass"`
		ChartVersion       string            `yaml:"chartVersion"`
		Version            string            `yaml:"version"`
		DeletionProtection bool              `yaml:"deletionProtection"`
		DeletionPolicy     string            `yaml:"deletionPolicy"`
		Dependencies       []string          `yaml:"dependencies"`
		Parameters         map[string]string `yaml:"parameters"`
		LifecycleControl   struct {
			DrainTimeoutSeconds int  `yaml:"drainTimeoutSeconds"`
			Cascade             bool `yaml:"cascade"`
		} `yaml:"lifecycleControl"`
	} `yaml:"spec"`
	FilePath string `yaml:"-"`
}

// ValidationResult records validation findings.
type ValidationResult struct {
	Valid    bool
	Errors   []string
	Warnings []string
}

// Linter executes 3-pass validation on component manifests.
type Linter struct {
	rules RulesConfig
}

// NewLinter creates a new Linter instance.
func NewLinter(rules RulesConfig) *Linter {
	return &Linter{rules: rules}
}

// LoadRules parses rules.yaml from a file path.
func LoadRules(path string) (RulesConfig, error) {
	var cfg RulesConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read rules config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse rules yaml %q: %w", path, err)
	}
	return cfg, nil
}

// ValidateDirectory scans a manifests directory and runs all 3 validation passes.
func (l *Linter) ValidateDirectory(dir string) (*ValidationResult, error) {
	manifests, err := l.loadManifests(dir)
	if err != nil {
		return nil, err
	}

	res := &ValidationResult{Valid: true}

	// Pass 1: Syntax & Scope Validation
	l.runPass1(manifests, res)

	// Pass 2: DAG Dependency & Cycle Detection
	l.runPass2(manifests, res)

	// Pass 3: Static Compatibility Matrix
	l.runPass3(manifests, res)

	res.Valid = len(res.Errors) == 0
	return res, nil
}

func (l *Linter) loadManifests(dir string) ([]ComponentManifest, error) {
	var manifests []ComponentManifest
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if strings.HasSuffix(path, "values.yaml") {
			parentDir := filepath.Base(filepath.Dir(path))
			if parentDir != "components" && parentDir != "." {
				var valMap struct {
					Enabled            *bool             `yaml:"enabled"`
					ComponentClass     string            `yaml:"componentClass"`
					DeletionProtection *bool             `yaml:"deletionProtection"`
					Dependencies       []string          `yaml:"dependencies"`
					Parameters         map[string]string `yaml:"parameters"`
					LifecycleControl   struct {
						DrainTimeoutSeconds int `yaml:"drainTimeoutSeconds"`
					} `yaml:"lifecycleControl"`
				}
				if err := yaml.Unmarshal(data, &valMap); err == nil {
					en := true
					if valMap.Enabled != nil {
						en = *valMap.Enabled
					}
					dp := true
					if valMap.DeletionProtection != nil {
						dp = *valMap.DeletionProtection
					}
					var m ComponentManifest
					m.Kind = "Component"
					m.Metadata.Name = parentDir
					m.Spec.Enabled = en
					m.Spec.ComponentClass = valMap.ComponentClass
					m.Spec.DeletionProtection = dp
					m.Spec.Dependencies = valMap.Dependencies
					m.Spec.Parameters = valMap.Parameters
					m.Spec.LifecycleControl.DrainTimeoutSeconds = valMap.LifecycleControl.DrainTimeoutSeconds
					m.FilePath = path
					manifests = append(manifests, m)
				}
			}
		}

		var m ComponentManifest
		if err := yaml.Unmarshal(data, &m); err == nil && m.Kind == "Component" {
			m.FilePath = path
			manifests = append(manifests, m)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error reading directory %q: %w", dir, err)
	}
	return manifests, nil
}

// Pass 1: Syntax, OpenAPI Scope & Static Policies
func (l *Linter) runPass1(manifests []ComponentManifest, res *ValidationResult) {
	for _, m := range manifests {
		if m.Metadata.Name == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: metadata.name must not be empty", m.FilePath))
		}

		if l.rules.StaticChecks.EnforceDeletionProtection && !m.Spec.DeletionProtection {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: spec.deletionProtection must be true on manifest creation", m.FilePath))
		}

		if maxDrain := l.rules.StaticChecks.MaxDrainTimeoutSeconds; maxDrain > 0 {
			if m.Spec.LifecycleControl.DrainTimeoutSeconds > maxDrain {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: drainTimeoutSeconds (%d) exceeds max allowed (%d)", m.FilePath, m.Spec.LifecycleControl.DrainTimeoutSeconds, maxDrain))
			}
		}
	}
}

// Pass 2: DAG Dependency Resolution & Cycle Detection
func (l *Linter) runPass2(manifests []ComponentManifest, res *ValidationResult) {
	compMap := make(map[string]ComponentManifest)
	for _, m := range manifests {
		compMap[m.Metadata.Name] = m
	}

	// 1. Verify all dependencies exist and are enabled if dependent is enabled
	for _, m := range manifests {
		if m.Spec.Enabled {
			for _, dep := range m.Spec.Dependencies {
				depComp, exists := compMap[dep]
				if !exists {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: depends on missing component %q", m.FilePath, dep))
				} else if !depComp.Spec.Enabled {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: component %q is enabled but depends on disabled component %q", m.FilePath, m.Metadata.Name, dep))
				}
			}
		}
	}

	// 2. Topological sort to detect cycles
	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	var hasCycle bool

	var visit func(node string, path []string)
	visit = func(node string, path []string) {
		visited[node] = 1
		path = append(path, node)

		m, exists := compMap[node]
		if exists {
			for _, dep := range m.Spec.Dependencies {
				if visited[dep] == 1 {
					hasCycle = true
					res.Errors = append(res.Errors, fmt.Sprintf("Cyclic dependency detected: %s -> %s", strings.Join(path, " -> "), dep))
				} else if visited[dep] == 0 {
					visit(dep, path)
				}
			}
		}
		visited[node] = 2
	}

	for name := range compMap {
		if visited[name] == 0 {
			visit(name, nil)
		}
	}
	_ = hasCycle
}

// Pass 3: Static Compatibility Matrix
func (l *Linter) runPass3(manifests []ComponentManifest, res *ValidationResult) {
	compMap := make(map[string]ComponentManifest)
	for _, m := range manifests {
		compMap[m.Metadata.Name] = m
	}

	for _, rule := range l.rules.CompatibilityMatrix.ComponentDependencies {
		if comp, exists := compMap[rule.Component]; exists && comp.Spec.Enabled {
			for _, req := range rule.Requires {
				dep, ok := compMap[req.Component]
				if !ok || !dep.Spec.Enabled {
					res.Errors = append(res.Errors, fmt.Sprintf("Compatibility matrix violation: %q requires %q (%s) to be present and enabled", rule.Component, req.Component, req.VersionRange))
				} else if req.VersionRange != "" && dep.Spec.ChartVersion != "" {
					if !checkSemverConstraint(dep.Spec.ChartVersion, req.VersionRange) {
						res.Errors = append(res.Errors, fmt.Sprintf("Compatibility matrix violation: %q requires %q version %s (found %s)", rule.Component, req.Component, req.VersionRange, dep.Spec.ChartVersion))
					}
				}
			}
		}
	}
}

type semver struct {
	major int
	minor int
	patch int
}

func parseSemver(v string) (semver, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return semver{}, false
	}
	var res semver
	var err error
	res.major, err = strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, false
	}
	if len(parts) > 1 {
		res.minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		pStr := strings.Split(parts[2], "-")[0]
		res.patch, _ = strconv.Atoi(pStr)
	}
	return res, true
}

func (s semver) compare(other semver) int {
	if s.major != other.major {
		if s.major < other.major {
			return -1
		}
		return 1
	}
	if s.minor != other.minor {
		if s.minor < other.minor {
			return -1
		}
		return 1
	}
	if s.patch != other.patch {
		if s.patch < other.patch {
			return -1
		}
		return 1
	}
	return 0
}

func checkSemverConstraint(verStr, rangeStr string) bool {
	if rangeStr == "" || rangeStr == "*" || verStr == "" {
		return true
	}
	v, ok := parseSemver(verStr)
	if !ok {
		return true
	}
	rangeStr = strings.TrimSpace(rangeStr)
	op, targetStr := extractOperator(rangeStr)
	t, ok := parseSemver(targetStr)
	if !ok {
		return true
	}
	cmp := v.compare(t)
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	case "^":
		return v.major == t.major && cmp >= 0
	default:
		return cmp == 0
	}
}

func extractOperator(rangeStr string) (string, string) {
	for _, op := range []string{">=", "<=", ">", "<", "^", "="} {
		if strings.HasPrefix(rangeStr, op) {
			return op, strings.TrimSpace(strings.TrimPrefix(rangeStr, op))
		}
	}
	return "", rangeStr
}
