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

package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// Version represents a parsed Semantic Version 2.0.0 structure.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	PreRelease []string
	Build      string
	Raw        string
}

// normalize prepares a version string for golang.org/x/mod/semver by ensuring the 'v' prefix
// and validating strict SemVer 2.0.0 (3 numeric segments: major.minor.patch).
func normalize(v string) (string, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", errors.New("empty version string")
	}
	if !strings.HasPrefix(s, "v") && !strings.HasPrefix(s, "V") {
		s = "v" + s
	} else if strings.HasPrefix(s, "V") {
		s = "v" + s[1:]
	}
	if !semver.IsValid(s) {
		return "", fmt.Errorf("invalid semver 2.0.0 string %q", v)
	}
	core := strings.TrimPrefix(s, "v")
	if idx := strings.IndexAny(core, "-+"); idx != -1 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid semver 2.0.0 string %q: expected major.minor.patch", v)
	}
	return s, nil
}

// Parse parses a semantic version string conforming to SemVer 2.0.0 with optional leading 'v' or 'V'.
func Parse(v string) (*Version, error) {
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}

	majStr := strings.TrimPrefix(semver.Major(norm), "v")
	maj, err := strconv.ParseUint(majStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid major version in %q: %w", v, err)
	}

	majMin := strings.TrimPrefix(semver.MajorMinor(norm), "v")
	parts := strings.Split(majMin, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid major.minor in %q", v)
	}
	min, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid minor version in %q: %w", v, err)
	}

	canonical := semver.Canonical(norm)
	pre := semver.Prerelease(canonical)
	build := semver.Build(norm)

	core := strings.TrimPrefix(canonical, "v")
	if idx := strings.IndexAny(core, "-+"); idx != -1 {
		core = core[:idx]
	}
	coreParts := strings.Split(core, ".")
	if len(coreParts) != 3 {
		return nil, fmt.Errorf("expected 3 segments in %q", v)
	}
	patch, err := strconv.ParseUint(coreParts[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid patch version in %q: %w", v, err)
	}

	var preList []string
	if pre != "" {
		preList = strings.Split(strings.TrimPrefix(pre, "-"), ".")
	}

	return &Version{
		Major:      maj,
		Minor:      min,
		Patch:      patch,
		PreRelease: preList,
		Build:      strings.TrimPrefix(build, "+"),
		Raw:        v,
	}, nil
}

// Compare compares two version strings according to SemVer 2.0.0 §11.
// Returns -1 if v1 < v2, 0 if v1 == v2, and 1 if v1 > v2.
func Compare(v1, v2 string) (int, error) {
	n1, err := normalize(v1)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %q: %w", v1, err)
	}
	n2, err := normalize(v2)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %q: %w", v2, err)
	}
	return semver.Compare(n1, n2), nil
}

// Compare compares the version to another Version struct according to SemVer 2.0.0 §11.
// Build metadata is ignored when determining precedence.
func (v *Version) Compare(other *Version) int {
	if v == nil && other == nil {
		return 0
	}
	if v == nil {
		return -1
	}
	if other == nil {
		return 1
	}
	cmp, _ := Compare(v.String(), other.String())
	return cmp
}

// Equal returns true if the two versions have identical precedence.
func (v *Version) Equal(other *Version) bool {
	return v.Compare(other) == 0
}

// LessThan returns true if v has lower precedence than other.
func (v *Version) LessThan(other *Version) bool {
	return v.Compare(other) < 0
}

// GreaterThan returns true if v has higher precedence than other.
func (v *Version) GreaterThan(other *Version) bool {
	return v.Compare(other) > 0
}

// String returns the normalized canonical string representation of the version.
func (v *Version) String() string {
	if v == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch))
	if len(v.PreRelease) > 0 {
		sb.WriteString("-")
		sb.WriteString(strings.Join(v.PreRelease, "."))
	}
	if v.Build != "" {
		sb.WriteString("+")
		sb.WriteString(v.Build)
	}
	return sb.String()
}

// IsPatchUpdate checks if target is a strictly higher patch version of current with identical Major and Minor.
func IsPatchUpdate(current, target string) (bool, error) {
	nCur, err := normalize(current)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", current, err)
	}
	nTarget, err := normalize(target)
	if err != nil {
		return false, fmt.Errorf("invalid target version %q: %w", target, err)
	}
	if semver.MajorMinor(nCur) != semver.MajorMinor(nTarget) {
		return false, nil
	}
	// Disallow stable to pre-release update
	if semver.Prerelease(nCur) == "" && semver.Prerelease(nTarget) != "" {
		return false, nil
	}
	return semver.Compare(nTarget, nCur) > 0, nil
}

// SatisfiesMinVersion checks if version is greater than or equal to minVersion.
func SatisfiesMinVersion(version, minVersion string) (bool, error) {
	nVer, err := normalize(version)
	if err != nil {
		return false, fmt.Errorf("invalid version %q: %w", version, err)
	}
	nMin, err := normalize(minVersion)
	if err != nil {
		return false, fmt.Errorf("invalid minVersion %q: %w", minVersion, err)
	}
	return semver.Compare(nVer, nMin) >= 0, nil
}
