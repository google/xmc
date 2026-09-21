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

package componentenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// chartMarker is a file that only exists at the repository root and is required by these tests.
var chartMarker = filepath.Join("deploy", "helm", "Chart.yaml")

// repoRoot locates the repository root so manifests can be read by a stable relative path.
//
// Under Bazel the sources live in the runfiles tree; under `go test` the working directory is the
// package directory, so parent directories are probed as a fallback.
func repoRoot() (string, error) {
	var candidates []string
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		if workspace := os.Getenv("TEST_WORKSPACE"); workspace != "" {
			candidates = append(candidates, filepath.Join(srcDir, workspace))
		}
	}
	candidates = append(candidates, ".", "..", filepath.Join("..", ".."), filepath.Join("..", "..", ".."))
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, chartMarker)); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no directory among %v contains %s", candidates, chartMarker)
}
