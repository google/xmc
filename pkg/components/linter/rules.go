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

// Package linter provides a static linter engine for validating XMC component manifests,
// dependency DAG topology, and SemVer compatibility rules.
package linter

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StaticChecksConfig specifies toggles and limits for Pass 1 structural and security checks.
type StaticChecksConfig struct {
	EnforceNamespaceScope     bool `yaml:"enforceNamespaceScope" json:"enforceNamespaceScope"`
	EnforceDeletionProtection bool `yaml:"enforceDeletionProtection" json:"enforceDeletionProtection"`
	MaxDrainTimeoutSeconds    int  `yaml:"maxDrainTimeoutSeconds" json:"maxDrainTimeoutSeconds"`
}

// DependencyRequirement defines a required dependency and its SemVer constraint.
type DependencyRequirement struct {
	Component string `yaml:"component" json:"component"`
	Version   string `yaml:"version" json:"version"`
	Optional  bool   `yaml:"optional,omitempty" json:"optional,omitempty"`
	Reason    string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// ComponentDependencyRule specifies version requirements for a component's dependencies.
type ComponentDependencyRule struct {
	Component        string                  `yaml:"component" json:"component"`
	ComponentVersion string                  `yaml:"componentVersion,omitempty" json:"componentVersion,omitempty"`
	Requires         []DependencyRequirement `yaml:"requires" json:"requires"`
}

// PlatformRequirementConfig specifies target Kubernetes and CRD requirements for a component.
type PlatformRequirementConfig struct {
	KubernetesVersion string   `yaml:"kubernetesVersion,omitempty" json:"kubernetesVersion,omitempty"`
	MinK8sVersion     string   `yaml:"minK8sVersion,omitempty" json:"minK8sVersion,omitempty"`
	MaxK8sVersion     string   `yaml:"maxK8sVersion,omitempty" json:"maxK8sVersion,omitempty"`
	RequiredCRDs      []string `yaml:"requiredCRDs,omitempty" json:"requiredCRDs,omitempty"`
}

// PlatformIntegrationRule specifies platform requirements for a specific component.
type PlatformIntegrationRule struct {
	Component string                    `yaml:"component" json:"component"`
	Platform  PlatformRequirementConfig `yaml:"platform" json:"platform"`
}

// CompatibilityMatrixConfig holds matrix rules for component dependencies and platform constraints.
type CompatibilityMatrixConfig struct {
	ComponentDependencies []ComponentDependencyRule `yaml:"componentDependencies" json:"componentDependencies"`
	PlatformRequirements  []PlatformIntegrationRule `yaml:"platformRequirements" json:"platformRequirements"`
}

// RulesConfig is the top-level configuration for the static linter engine.
type RulesConfig struct {
	StaticChecks        StaticChecksConfig        `yaml:"staticChecks" json:"staticChecks"`
	CompatibilityMatrix CompatibilityMatrixConfig `yaml:"compatibilityMatrix" json:"compatibilityMatrix"`
}

// LoadRules loads and parses a rules configuration YAML file from the given file path.
func LoadRules(path string) (*RulesConfig, error) {
	if path == "" {
		return nil, fmt.Errorf("rules file path cannot be empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file %q: %w", path, err)
	}
	return ParseRules(data)
}

// ParseRules parses raw YAML bytes into a RulesConfig structure.
func ParseRules(data []byte) (*RulesConfig, error) {
	if len(data) == 0 {
		return &RulesConfig{}, nil
	}
	var cfg RulesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse rules yaml: %w", err)
	}
	return &cfg, nil
}
