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
	"testing"

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
				ForbiddenKinds:            nil,
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name:     "comments only yaml returns empty config without error",
			yamlData: "# only comments\n# nothing else\n",
			wantErr:  false,
			wantChecks: linter.StaticChecksConfig{
				ForbiddenKinds:            nil,
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name:     "whitespace only yaml returns empty config without error",
			yamlData: "   \n  \n",
			wantErr:  false,
			wantChecks: linter.StaticChecksConfig{
				ForbiddenKinds:            nil,
				EnforceDeletionProtection: false,
				MaxDrainTimeoutSeconds:    0,
			},
		},
		{
			name: "valid static checks config",
			yamlData: `
staticChecks:
  forbiddenKinds:
    - ClusterRole
    - StorageClass
  enforceDeletionProtection: true
  maxDrainTimeoutSeconds: 300
`,
			wantErr: false,
			wantChecks: linter.StaticChecksConfig{
				ForbiddenKinds:            []string{"ClusterRole", "StorageClass"},
				EnforceDeletionProtection: true,
				MaxDrainTimeoutSeconds:    300,
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
			if len(cfg.StaticChecks.ForbiddenKinds) != len(tc.wantChecks.ForbiddenKinds) {
				t.Errorf("ForbiddenKinds length = %v, want %v", len(cfg.StaticChecks.ForbiddenKinds), len(tc.wantChecks.ForbiddenKinds))
			}
			if cfg.StaticChecks.EnforceDeletionProtection != tc.wantChecks.EnforceDeletionProtection {
				t.Errorf("EnforceDeletionProtection = %v, want %v", cfg.StaticChecks.EnforceDeletionProtection, tc.wantChecks.EnforceDeletionProtection)
			}
			if cfg.StaticChecks.MaxDrainTimeoutSeconds != tc.wantChecks.MaxDrainTimeoutSeconds {
				t.Errorf("MaxDrainTimeoutSeconds = %v, want %v", cfg.StaticChecks.MaxDrainTimeoutSeconds, tc.wantChecks.MaxDrainTimeoutSeconds)
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
  forbiddenKinds:
    - ClusterRole
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
		if len(cfg.StaticChecks.ForbiddenKinds) != 1 || cfg.StaticChecks.ForbiddenKinds[0] != "ClusterRole" {
			t.Errorf("expected ForbiddenKinds to contain ClusterRole")
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
