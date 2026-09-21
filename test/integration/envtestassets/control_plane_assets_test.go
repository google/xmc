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

package envtestassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBinary creates a file named kube-apiserver in dir with the given permissions.
func writeBinary(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, kubeAPIServerBinary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatalf("writing fake binary: %v", err)
	}
	// WriteFile honors umask, so set the mode explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod fake binary: %v", err)
	}
}

func TestDirPrefersKubebuilderAssets(t *testing.T) {
	dir := t.TempDir()
	writeBinary(t, dir, 0o755)
	t.Setenv("KUBEBUILDER_ASSETS", dir)
	// A bogus runfiles location must be ignored when KUBEBUILDER_ASSETS is usable.
	t.Setenv("TEST_SRCDIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TEST_WORKSPACE", "_main")

	got, err := Dir()

	if err != nil {
		t.Fatalf("Dir() returned unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
}

func TestDirFallsBackToRunfiles(t *testing.T) {
	srcDir := t.TempDir()
	const workspace = "_main"
	assetsDir := filepath.Join(srcDir, workspace, runfilesAssetsDir)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("creating fake runfiles tree: %v", err)
	}
	writeBinary(t, assetsDir, 0o755)
	t.Setenv("KUBEBUILDER_ASSETS", "")
	t.Setenv("TEST_SRCDIR", srcDir)
	t.Setenv("TEST_WORKSPACE", workspace)

	got, err := Dir()

	if err != nil {
		t.Fatalf("Dir() returned unexpected error: %v", err)
	}
	if got != assetsDir {
		t.Errorf("Dir() = %q, want %q", got, assetsDir)
	}
}

func TestDirErrors(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares the environment and returns nothing; each case runs in isolation.
		setup func(t *testing.T)
		// wantMessage is a substring the error must contain.
		wantMessage string
	}{
		{
			name: "kubebuilder assets directory has no kube-apiserver",
			setup: func(t *testing.T) {
				t.Setenv("KUBEBUILDER_ASSETS", t.TempDir())
			},
			wantMessage: "KUBEBUILDER_ASSETS is set but unusable",
		},
		{
			name: "kube-apiserver is present but not executable",
			setup: func(t *testing.T) {
				dir := t.TempDir()
				writeBinary(t, dir, 0o644)
				t.Setenv("KUBEBUILDER_ASSETS", dir)
			},
			wantMessage: "is not an executable file",
		},
		{
			name: "not running under bazel and no assets configured",
			setup: func(t *testing.T) {
				t.Setenv("KUBEBUILDER_ASSETS", "")
				t.Setenv("TEST_SRCDIR", "")
			},
			wantMessage: "TEST_SRCDIR is unset",
		},
		{
			name: "bazel runfiles tree lacks the staged binaries",
			setup: func(t *testing.T) {
				t.Setenv("KUBEBUILDER_ASSETS", "")
				t.Setenv("TEST_SRCDIR", t.TempDir())
				t.Setenv("TEST_WORKSPACE", "_main")
			},
			wantMessage: "bazel runfiles assets directory is unusable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)

			got, err := Dir()

			if err == nil {
				t.Fatalf("Dir() = %q, want an error", got)
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Errorf("Dir() error = %q, want it to contain %q", err.Error(), test.wantMessage)
			}
		})
	}
}
