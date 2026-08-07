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

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListConfigs_Success(t *testing.T) {
	tempDir := t.TempDir()

	// Mock ESS config
	essDir := filepath.Join(tempDir, "ess")
	if err := os.Mkdir(essDir, 0755); err != nil {
		t.Fatalf("failed to create ess dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(essDir, "ess_key"), []byte("ess_val"), 0644); err != nil {
		t.Fatalf("failed to write ess config: %v", err)
	}

	// Mock Health Service config (new requirement)
	hsDir := filepath.Join(tempDir, "health-service")
	if err := os.Mkdir(hsDir, 0755); err != nil {
		t.Fatalf("failed to create hs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hsDir, "hs_key"), []byte("hs_val"), 0644); err != nil {
		t.Fatalf("failed to write hs config: %v", err)
	}

	groups, err := listConfigs(tempDir)
	if err != nil {
		t.Fatalf("listConfigs failed: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("Expected 2 config groups, got %d", len(groups))
	}

	expected := map[string]map[string]string{
		"Experiment State Server Config": {"ess_key": "ess_val"},
		"Health Service Config":          {"hs_key": "hs_val"},
	}

	for _, g := range groups {
		expConfigs, ok := expected[g.DisplayName]
		if !ok {
			t.Errorf("Unexpected group: %q", g.DisplayName)
			continue
		}
		if len(g.Configs) != len(expConfigs) {
			t.Errorf("Group %q: expected %d configs, got %d", g.DisplayName, len(expConfigs), len(g.Configs))
			continue
		}
		for _, c := range g.Configs {
			val, ok := expConfigs[c.Key]
			if !ok {
				t.Errorf("Group %q: unexpected config key: %q", g.DisplayName, c.Key)
				continue
			}
			if c.Value != val {
				t.Errorf("Group %q, key %q: expected value %q, got %q", g.DisplayName, c.Key, val, c.Value)
			}
		}
	}
}

func TestListConfigs_HiddenFilesAndDirsIgnored(t *testing.T) {
	tempDir := t.TempDir()

	essDir := filepath.Join(tempDir, "ess")
	if err := os.Mkdir(essDir, 0755); err != nil {
		t.Fatalf("failed to create ess dir: %v", err)
	}
	// Normal file
	if err := os.WriteFile(filepath.Join(essDir, "valid_key"), []byte("val"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	// Hidden file
	if err := os.WriteFile(filepath.Join(essDir, ".hidden_key"), []byte("val"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	// Subdirectory
	if err := os.Mkdir(filepath.Join(essDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	groups, err := listConfigs(tempDir)
	if err != nil {
		t.Fatalf("listConfigs failed: %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("Expected 1 config group, got %d", len(groups))
	}

	configs := groups[0].Configs
	if len(configs) != 1 {
		t.Fatalf("Expected 1 config in group, got %d", len(configs))
	}

	if configs[0].Key != "valid_key" {
		t.Errorf("Expected only 'valid_key' to be returned, got %q", configs[0].Key)
	}
}

func TestListConfigs_MissingDirsSkipped(t *testing.T) {
	tempDir := t.TempDir()
	// No subdirectories created

	groups, err := listConfigs(tempDir)
	if err != nil {
		t.Fatalf("listConfigs failed: %v", err)
	}

	if len(groups) != 0 {
		t.Errorf("Expected 0 config groups for empty directory, got %d", len(groups))
	}
}
