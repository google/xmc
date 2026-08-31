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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_CLI(t *testing.T) {
	t.Parallel()

	t.Run("missing manifests-dir or invalid flag returns exit 1", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		if run([]string{}, &out, &errOut) != 1 || !strings.Contains(errOut.String(), "--manifests-dir is required") {
			t.Errorf("expected exit 1 for missing dir")
		}
		if run([]string{"--invalid"}, &out, &errOut) != 1 {
			t.Errorf("expected exit 1 for invalid flag")
		}
	})

	t.Run("valid directory returns exit 0 for text and json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "c1.yaml"), []byte("apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n"), 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		var outText, errText, outJSON, errJSON bytes.Buffer
		if run([]string{"--manifests-dir=" + dir}, &outText, &errText) != 0 || !strings.Contains(outText.String(), "passed static validation") {
			t.Errorf("expected text pass, got %q", outText.String())
		}
		if run([]string{"--manifests-dir=" + dir, "--output=json"}, &outJSON, &errJSON) != 0 || !strings.Contains(outJSON.String(), `"valid": true`) {
			t.Errorf("expected json pass, got %q", outJSON.String())
		}
	})

	t.Run("directory with error returns exit 1 for text and json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("{ unclosed mapping"), 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		var outText, errText, outJSON, errJSON bytes.Buffer
		if run([]string{"--manifests-dir=" + dir}, &outText, &errText) != 1 || !strings.Contains(errText.String(), "[ERROR]") {
			t.Errorf("expected exit 1 with validation error in text mode")
		}
		if run([]string{"--manifests-dir=" + dir, "--output=json"}, &outJSON, &errJSON) != 1 || !strings.Contains(outJSON.String(), `"valid": false`) {
			t.Errorf("expected exit 1 with validation error in json mode")
		}
	})

	t.Run("nonexistent directory returns exit 1 on execution error", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		if run([]string{"--manifests-dir=/tmp/path/does/not/exist/998877"}, &out, &errOut) != 1 || !strings.Contains(errOut.String(), "Error running linter") {
			t.Errorf("expected exit 1 for invalid path, got: %s", errOut.String())
		}
	})

	t.Run("invalid output format returns exit 1", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var out, errOut bytes.Buffer
		if run([]string{"--manifests-dir=" + dir, "--output=xml"}, &out, &errOut) != 1 || !strings.Contains(errOut.String(), "invalid --output format") {
			t.Errorf("expected exit 1 for invalid output format")
		}
	})

	t.Run("rules config flag validation", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "c1.yaml"), []byte("apiVersion: components.xmc.dev/v1alpha1\nkind: Component\nmetadata: { name: c1 }\nspec: { version: 1.0.0 }\n"), 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		rulesPath := filepath.Join(dir, "rules.yaml")
		if err := os.WriteFile(rulesPath, []byte("staticChecks:\n  maxDrainTimeoutSeconds: 300\n"), 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		var out, errOut bytes.Buffer
		if run([]string{"--manifests-dir=" + dir, "--rules-config=" + rulesPath}, &out, &errOut) != 0 {
			t.Errorf("expected success with valid rules config, got: %s", errOut.String())
		}
		var outErr, errOutErr bytes.Buffer
		if run([]string{"--manifests-dir=" + dir, "--rules-config=" + filepath.Join(dir, "nonexistent.yaml")}, &outErr, &errOutErr) != 1 || !strings.Contains(errOutErr.String(), "Error loading rules") {
			t.Errorf("expected error with nonexistent rules config")
		}
	})
}
