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

	"gopkg.in/yaml.v3"
)

// StaticChecksConfig defines static guardrails and security checks.
type StaticChecksConfig struct {
	EnforceNamespaceScope     bool `yaml:"enforceNamespaceScope" json:"enforceNamespaceScope"`
	EnforceDeletionProtection bool `yaml:"enforceDeletionProtection" json:"enforceDeletionProtection"`
	MaxDrainTimeoutSeconds    int  `yaml:"maxDrainTimeoutSeconds" json:"maxDrainTimeoutSeconds"`
}

// RulesConfig is the top-level structure loaded from rules.yaml.
type RulesConfig struct {
	StaticChecks StaticChecksConfig `yaml:"staticChecks" json:"staticChecks"`
}

// LoadRules reads and parses a rules configuration from the specified file path.
func LoadRules(path string) (*RulesConfig, error) {
	if path == "" {
		return nil, fmt.Errorf("rules path cannot be empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file %q: %w", path, err)
	}
	return ParseRules(data)
}

// ParseRules unmarshals rules configuration from raw YAML bytes.
func ParseRules(data []byte) (*RulesConfig, error) {
	var cfg RulesConfig
	if len(data) == 0 {
		return &cfg, nil
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse rules configuration: %w", err)
	}
	return &cfg, nil
}
