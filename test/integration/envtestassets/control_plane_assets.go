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

// Package envtestassets locates the Kubernetes control-plane binaries (kube-apiserver, etcd,
// kubectl) that controller-runtime's envtest boots for integration tests.
//
// Under Bazel the binaries are supplied as checksummed data dependencies and resolved from the
// runfiles tree, which keeps the tests hermetic: no Docker daemon and no network access are
// required. Outside Bazel the standard KUBEBUILDER_ASSETS environment variable is honored so
// that `go test` still works for developers running setup-envtest locally.
package envtestassets

import (
	"fmt"
	"os"
	"path/filepath"
)

// kubeAPIServerBinary is the binary whose presence marks a directory as a valid assets directory.
const kubeAPIServerBinary = "kube-apiserver"

// runfilesAssetsDir is the workspace-relative location that //test/integration:envtest_control_plane
// copies the control-plane binaries into.
const runfilesAssetsDir = "test/integration/envtest_control_plane"

// Dir returns the directory holding the envtest control-plane binaries.
//
// Resolution order:
//  1. KUBEBUILDER_ASSETS, so a developer-managed setup-envtest installation wins.
//  2. The Bazel runfiles tree, for hermetic `bazel test` runs.
//
// It returns an error describing both lookups when no usable directory is found, rather than
// letting envtest fail later with an opaque "fork/exec" message.
func Dir() (string, error) {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		if err := verify(dir); err != nil {
			return "", fmt.Errorf("KUBEBUILDER_ASSETS is set but unusable: %w", err)
		}
		return dir, nil
	}
	dir, err := runfilesDir()
	if err != nil {
		return "", fmt.Errorf("KUBEBUILDER_ASSETS is unset and the bazel runfiles lookup failed: %w", err)
	}
	if err := verify(dir); err != nil {
		return "", fmt.Errorf("bazel runfiles assets directory is unusable: %w", err)
	}
	return dir, nil
}

// runfilesDir derives the assets directory from the Bazel test runfiles environment.
func runfilesDir() (string, error) {
	srcDir := os.Getenv("TEST_SRCDIR")
	if srcDir == "" {
		return "", fmt.Errorf("TEST_SRCDIR is unset, so the test is not running under bazel")
	}
	workspace := os.Getenv("TEST_WORKSPACE")
	if workspace == "" {
		return "", fmt.Errorf("TEST_SRCDIR is set but TEST_WORKSPACE is unset")
	}
	return filepath.Join(srcDir, workspace, runfilesAssetsDir), nil
}

// verify reports whether dir contains an executable kube-apiserver binary.
func verify(dir string) error {
	path := filepath.Join(dir, kubeAPIServerBinary)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%q is not an executable file", path)
	}
	return nil
}
