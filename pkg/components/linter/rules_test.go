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

package linter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/components/linter"
)

func TestParseRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yamlData   string
		wantErr    bool
		wantChecks linter.StaticChecksConfig
	}{
		{
			name:     "empty yaml",
			yamlData: "",
			wantErr:  false,
			wantChecks: linter.StaticChecksConfig{
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name:     "comments only yaml returns empty config without error",
			yamlData: "# only comments\n# nothing else\n",
			wantErr:  false,
			wantChecks: linter.StaticChecksConfig{
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name:     "whitespace only yaml returns empty config without error",
			yamlData: "   \n  \n",
			wantErr:  false,
			wantChecks: linter.StaticChecksConfig{
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name: "valid static checks config",
			yamlData: `
staticChecks:
  enforceDeletionProtection: true
  maxDrainTimeoutSeconds: 300
  allowExperimental: false
`,
			wantErr: false,
			wantChecks: linter.StaticChecksConfig{
				EnforceDeletionProtection: true,
				MaxDrainTimeoutSeconds:    300,
				AllowExperimental:         boolPtr(false),
			},
		},
		{
			name:     "invalid yaml",
			yamlData: ":::invalid",
			wantErr:  true,
		},
		{
			name:     "unknown fields rejected",
			yamlData: "unknownField: true\n",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := linter.ParseRules([]byte(tc.yamlData))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRules() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRules() unexpected error: %v", err)
			}
			if cfg.StaticChecks.EnforceDeletionProtection != tc.wantChecks.EnforceDeletionProtection {
				t.Errorf("EnforceDeletionProtection = %v, want %v", cfg.StaticChecks.EnforceDeletionProtection, tc.wantChecks.EnforceDeletionProtection)
			}
			if cfg.StaticChecks.MaxDrainTimeoutSeconds != tc.wantChecks.MaxDrainTimeoutSeconds {
				t.Errorf("MaxDrainTimeoutSeconds = %v, want %v", cfg.StaticChecks.MaxDrainTimeoutSeconds, tc.wantChecks.MaxDrainTimeoutSeconds)
			}
			wantAllowed := tc.wantChecks.IsExperimentalAllowed()
			if got := cfg.StaticChecks.IsExperimentalAllowed(); got != wantAllowed {
				t.Errorf("IsExperimentalAllowed() = %v, want %v", got, wantAllowed)
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "rules.yaml")
	rulesContent := `
staticChecks:
  enforceDeletionProtection: true
  maxDrainTimeoutSeconds: 600
`
	if err := os.WriteFile(validFile, []byte(rulesContent), 0644); err != nil {
		t.Fatalf("failed to write test rules file: %v", err)
	}

	t.Run("valid file", func(t *testing.T) {
		t.Parallel()
		cfg, err := linter.LoadRules(validFile)
		if err != nil {
			t.Fatalf("LoadRules() unexpected error: %v", err)
		}
		if !cfg.StaticChecks.EnforceDeletionProtection {
			t.Errorf("expected EnforceDeletionProtection to be true")
		}
		if cfg.StaticChecks.MaxDrainTimeoutSeconds != 600 {
			t.Errorf("expected MaxDrainTimeoutSeconds to be 600, got %d", cfg.StaticChecks.MaxDrainTimeoutSeconds)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		_, err := linter.LoadRules(filepath.Join(tmpDir, "non_existent.yaml"))
		if err == nil {
			t.Fatalf("LoadRules() expected error for non-existent file, got nil")
		}
	})

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		_, err := linter.LoadRules("")
		if err == nil {
			t.Fatalf("LoadRules() expected error for empty path, got nil")
		}
	})
}

func TestParseRules_OrRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yamlData  string
		wantCfg   *linter.RulesConfig
		wantErrIn string
	}{
		{
			name: "valid 1-level or requirement and single requirement",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      componentVersion: ">=0.1.0"
      requires:
        - or:
            - component: redpanda
              version: ">=0.1.0"
            - component: pubsub
              version: ">=0.1.0"
          reason: "Either Redpanda or Pub/Sub is required"
        - component: clickhouse
          version: ">=0.1.0"
`,
			wantCfg: &linter.RulesConfig{
				CompatibilityMatrix: linter.CompatibilityMatrixConfig{
					ComponentDependencies: []linter.ComponentDependencyRule{
						{
							Component:        "ingestion-gateway",
							ComponentVersion: ">=0.1.0",
							Requires: []linter.DependencyRequirement{
								{
									Reason: "Either Redpanda or Pub/Sub is required",
									Or: []linter.DependencyRequirement{
										{Component: "redpanda", Version: ">=0.1.0"},
										{Component: "pubsub", Version: ">=0.1.0"},
									},
								},
								{
									Component: "clickhouse",
									Version:   ">=0.1.0",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "rejects setting both component and or",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - component: redpanda
          or:
            - component: pubsub
`,
			wantErrIn: "cannot specify both component",
		},
		{
			name: "rejects top-level version when or is used",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - version: ">=0.1.0"
          or:
            - component: redpanda
            - component: pubsub
`,
			wantErrIn: "cannot specify top-level version",
		},
		{
			name: "rejects empty requirement with neither component nor or",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - reason: "missing component and or"
`,
			wantErrIn: "must specify either component or a non-empty or list",
		},
		{
			name: "rejects empty or list",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - or: []
`,
			wantErrIn: "must specify either component or a non-empty or list",
		},
		{
			name: "rejects nested or inside or alternative",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - or:
            - component: redpanda
            - or:
                - component: pubsub
`,
			wantErrIn: "nested or is not supported",
		},
		{
			name: "rejects or alternative with empty component",
			yamlData: `
compatibilityMatrix:
  componentDependencies:
    - component: ingestion-gateway
      requires:
        - or:
            - version: ">=0.1.0"
`,
			wantErrIn: "or alternative in dependency requirement for \"ingestion-gateway\" must specify a non-empty component",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := linter.ParseRules([]byte(tc.yamlData))
			if tc.wantErrIn != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrIn) {
					t.Fatalf("ParseRules() error = %v, want substring %q", err, tc.wantErrIn)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRules() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantCfg, got); diff != "" {
				t.Errorf("ParseRules() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
