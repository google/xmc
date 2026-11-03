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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// StaticChecksConfig specifies toggles and limits for schema and security scope checks.
type StaticChecksConfig struct {
	EnforceDeletionProtection bool  `yaml:"enforceDeletionProtection" json:"enforceDeletionProtection"`
	MaxDrainTimeoutSeconds    int   `yaml:"maxDrainTimeoutSeconds" json:"maxDrainTimeoutSeconds"`
	AllowExperimental         *bool `yaml:"allowExperimental,omitempty" json:"allowExperimental,omitempty"`
}

// IsExperimentalAllowed reports whether experimental components are permitted by the profile rules.
// Defaults to true when AllowExperimental is nil.
func (s StaticChecksConfig) IsExperimentalAllowed() bool {
	return s.AllowExperimental == nil || *s.AllowExperimental
}

// DependencyRequirement defines a required dependency and its SemVer constraint,
// or a flat 1-level disjunction (Or) of alternative dependency requirements.
type DependencyRequirement struct {
	Component string                  `yaml:"component,omitempty" json:"component,omitempty"`
	Version   string                  `yaml:"version,omitempty" json:"version,omitempty"`
	Optional  bool                    `yaml:"optional,omitempty" json:"optional,omitempty"`
	Reason    string                  `yaml:"reason,omitempty" json:"reason,omitempty"`
	Or        []DependencyRequirement `yaml:"or,omitempty" json:"or,omitempty"`
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

// ParseRules parses raw YAML bytes into a RulesConfig structure with strict known field validation.
func ParseRules(data []byte) (*RulesConfig, error) {
	if len(data) == 0 {
		return &RulesConfig{}, nil
	}
	var cfg RulesConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &RulesConfig{}, nil
		}
		return nil, fmt.Errorf("failed to parse rules configuration: %w", err)
	}
	if err := validateRulesConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateRulesConfig(cfg *RulesConfig) error {
	for _, rule := range cfg.CompatibilityMatrix.ComponentDependencies {
		for _, req := range rule.Requires {
			if err := validateDependencyRequirement(rule.Component, req); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDependencyRequirement(ruleComp string, req DependencyRequirement) error {
	if len(req.Or) > 0 {
		return validateOrRequirement(ruleComp, req)
	}
	if strings.TrimSpace(req.Component) == "" {
		return fmt.Errorf("dependency requirement for %q must specify either component or a non-empty or list", ruleComp)
	}
	return nil
}

func validateOrRequirement(ruleComp string, req DependencyRequirement) error {
	if strings.TrimSpace(req.Component) != "" {
		return fmt.Errorf("dependency requirement for %q cannot specify both component %q and or", ruleComp, req.Component)
	}
	if strings.TrimSpace(req.Version) != "" {
		return fmt.Errorf("dependency requirement for %q cannot specify top-level version %q when or is used", ruleComp, req.Version)
	}
	for _, alt := range req.Or {
		if len(alt.Or) > 0 {
			return fmt.Errorf("nested or is not supported in dependency requirement for %q", ruleComp)
		}
		if strings.TrimSpace(alt.Component) == "" {
			return fmt.Errorf("or alternative in dependency requirement for %q must specify a non-empty component", ruleComp)
		}
	}
	return nil
}
