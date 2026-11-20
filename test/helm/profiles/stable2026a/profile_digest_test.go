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

package stable2026a_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	stable2026acharts "xmanager_cloud/deploy/profiles/stable-2026a"
	"xmanager_cloud/test/helm/profiles/profilecontract"
)

// lockedProfile Entry in deploy/profiles/locked_profiles.yaml.
type lockedProfile struct {
	LockedSince          string `yaml:"locked_since"`
	ChartDigest          string `yaml:"chart_digest"`
	EmbeddedChartsDigest string `yaml:"embedded_charts_digest"`
}

// loadLockedProfile reads the stable-2026a entry from deploy/profiles/locked_profiles.yaml.
func loadLockedProfile(t *testing.T, s profilecontract.Spec) lockedProfile {
	t.Helper()
	lockPath := filepath.Join(filepath.Dir(profilecontract.ChartDir(t, s)), "locked_profiles.yaml")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", lockPath, err)
	}
	var locks map[string]lockedProfile
	if err := yaml.Unmarshal(data, &locks); err != nil {
		t.Fatalf("failed to decode %s: %v", lockPath, err)
	}
	entry, ok := locks[s.ProfileName]
	if !ok {
		t.Fatalf("%s is missing locked profile entry %q", lockPath, s.ProfileName)
	}
	if entry.LockedSince == "" || entry.ChartDigest == "" || entry.EmbeddedChartsDigest == "" {
		t.Fatalf("%s entry %q has empty fields: %+v", lockPath, s.ProfileName, entry)
	}
	return entry
}

// TestStable2026a_ChartIsImmutable fails on any change to the stable-2026a chart files. stable-2026a shipped with
// XMC 1.0.0 and is GA-locked: clusters that installed it must keep getting exactly the same components, so changes
// go into a new profile under deploy/profiles instead.
func TestStable2026a_ChartIsImmutable(t *testing.T) {
	t.Parallel()
	s := stable2026a()
	lock := loadLockedProfile(t, s)
	got, err := chartDigest(os.DirFS(profilecontract.ChartDir(t, s)), isChartFile)
	if err != nil {
		t.Fatalf("chartDigest() failed: %v", err)
	}
	if got != lock.ChartDigest {
		t.Errorf("stable-2026a chart digest = %s, want %s (from deploy/profiles/locked_profiles.yaml, locked_since %s).\n"+
			"deploy/profiles/stable-2026a is GA-locked for XMC %s and must not change. Revert the edit and make the "+
			"change in a new profile under deploy/profiles instead.", got, lock.ChartDigest, lock.LockedSince, lock.LockedSince)
	}
}

// TestStable2026a_EmbeddedChartsAreImmutable fails on any change to the component charts that xmc-bridge embeds for
// stable-2026a, including a change of the //go:embed patterns that select them.
func TestStable2026a_EmbeddedChartsAreImmutable(t *testing.T) {
	t.Parallel()
	s := stable2026a()
	lock := loadLockedProfile(t, s)
	got, err := chartDigest(stable2026acharts.EmbeddedCharts, func(string) bool { return true })
	if err != nil {
		t.Fatalf("chartDigest() failed: %v", err)
	}
	if got != lock.EmbeddedChartsDigest {
		t.Errorf("stable-2026a embedded charts digest = %s, want %s (from deploy/profiles/locked_profiles.yaml, locked_since %s).\n"+
			"The charts xmc-bridge embeds for stable-2026a are GA-locked for XMC %s and must not change. Revert the "+
			"edit and make the change in a new profile under deploy/profiles instead.", got, lock.EmbeddedChartsDigest, lock.LockedSince, lock.LockedSince)
	}
}

// chartDigest returns a SHA-256 over the sorted paths and contents of the files in fsys whose names include accepts.
//
// For the chart directory, the digest assumes that the //deploy/profiles/stable-2026a:chart filegroup (Bazel
// runfiles) and the source directory (plain go test) hold the same files apart from those isChartFile skips. If the
// filegroup ever excludes another file, add it to isChartFile, or the digest would depend on how the test is run.
func chartDigest(fsys fs.FS, include func(name string) bool) (string, error) {
	files, err := chartFiles(fsys, include)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, rel := range files {
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return "", fmt.Errorf("failed to read chart file %s: %w", rel, err)
		}
		contentSum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s\x00%s\n", rel, hex.EncodeToString(contentSum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// chartFiles returns the sorted slash-separated paths of the files in fsys whose names include accepts.
func chartFiles(fsys fs.FS, include func(name string) bool) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !include(d.Name()) {
			return walkErr
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list chart files: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

// isChartFile reports whether a file named name belongs to the chart rather than to its build or documentation.
func isChartFile(name string) bool {
	return name != "BUILD.bazel" && name != "README.md" && !strings.HasSuffix(name, ".go")
}
