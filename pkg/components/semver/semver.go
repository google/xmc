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

// Package semver provides strict Semantic Versioning 2.0.0 parsing, validation,
// and precedence comparison utilities for XMC components.
package semver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	versionPrefix        = "v"
	requiredCoreSegments = 3
	prereleaseDelimiter  = "-"
	buildDelimiter       = "+"
	segmentDelimiter     = "."
	metadataDelimiters   = "-+"
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

type coreVersion struct {
	major uint64
	minor uint64
	patch uint64
}

// Parse parses a semantic version string conforming to SemVer 2.0.0 with optional leading 'v' or 'V'.
func Parse(v string) (*Version, error) {
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}

	canonical := semver.Canonical(norm)
	core, err := parseCoreSegments(canonical, v)
	if err != nil {
		return nil, err
	}

	return &Version{
		Major:      core.major,
		Minor:      core.minor,
		Patch:      core.patch,
		PreRelease: extractPreRelease(semver.Prerelease(canonical)),
		Build:      strings.TrimPrefix(semver.Build(norm), buildDelimiter),
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
	if v == other {
		return 0
	}
	if v == nil {
		return -1
	}
	if other == nil {
		return 1
	}
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}
	if len(v.PreRelease) == 0 && len(other.PreRelease) == 0 {
		return 0
	}
	return semver.Compare(versionPrefix+v.String(), versionPrefix+other.String())
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
	sb.WriteString(strconv.FormatUint(v.Major, 10))
	sb.WriteByte('.')
	sb.WriteString(strconv.FormatUint(v.Minor, 10))
	sb.WriteByte('.')
	sb.WriteString(strconv.FormatUint(v.Patch, 10))
	if len(v.PreRelease) > 0 {
		sb.WriteString(prereleaseDelimiter)
		sb.WriteString(strings.Join(v.PreRelease, segmentDelimiter))
	}
	if v.Build != "" {
		sb.WriteString(buildDelimiter)
		sb.WriteString(v.Build)
	}
	return sb.String()
}

// IsPatchUpdate checks if target is a strictly higher patch version of current with identical Major and Minor.
func IsPatchUpdate(current, target string) (bool, error) {
	curVer, err := Parse(current)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", current, err)
	}
	targetVer, err := Parse(target)
	if err != nil {
		return false, fmt.Errorf("invalid target version %q: %w", target, err)
	}
	if curVer.Major != targetVer.Major || curVer.Minor != targetVer.Minor {
		return false, nil
	}
	if len(curVer.PreRelease) == 0 && len(targetVer.PreRelease) > 0 {
		return false, nil
	}
	return targetVer.Compare(curVer) > 0, nil
}

// SatisfiesMinVersion checks if version is greater than or equal to minVersion.
func SatisfiesMinVersion(version, minVersion string) (bool, error) {
	ver, err := Parse(version)
	if err != nil {
		return false, fmt.Errorf("invalid version %q: %w", version, err)
	}
	minVer, err := Parse(minVersion)
	if err != nil {
		return false, fmt.Errorf("invalid minVersion %q: %w", minVersion, err)
	}
	return ver.Compare(minVer) >= 0, nil
}

// --- Helper Functions ---

// normalize prepares a version string for golang.org/x/mod/semver by ensuring the 'v' prefix
// and validating strict SemVer 2.0.0 (3 numeric segments: major.minor.patch).
func normalize(v string) (string, error) {
	s := ensureVPrefix(strings.TrimSpace(v))
	if s == "" {
		return "", errors.New("empty version string")
	}
	if !semver.IsValid(s) {
		return "", fmt.Errorf("invalid semver 2.0.0 string %q", v)
	}
	if err := validateCoreSegmentCount(s, v); err != nil {
		return "", err
	}
	return s, nil
}

// ensureVPrefix ensures the version string starts with a lowercase 'v'.
func ensureVPrefix(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "v") {
		return s
	}
	if strings.HasPrefix(s, "V") {
		return versionPrefix + s[1:]
	}
	return versionPrefix + s
}

// validateCoreSegmentCount checks that the version's core (excluding prerelease and build)
// contains exactly 3 numeric segments (major.minor.patch).
func validateCoreSegmentCount(normalized, raw string) error {
	core := strings.TrimPrefix(normalized, versionPrefix)
	if idx := strings.IndexAny(core, metadataDelimiters); idx != -1 {
		core = core[:idx]
	}
	if strings.Count(core, segmentDelimiter) != requiredCoreSegments-1 {
		return fmt.Errorf("invalid semver 2.0.0 string %q: expected major.minor.patch", raw)
	}
	return nil
}

// parseCoreSegments extracts and parses major, minor, and patch uint64 numbers from canonical semver.
func parseCoreSegments(canonical, raw string) (coreVersion, error) {
	core := strings.TrimPrefix(canonical, versionPrefix)
	if idx := strings.IndexAny(core, metadataDelimiters); idx != -1 {
		core = core[:idx]
	}
	parts := strings.Split(core, segmentDelimiter)
	if len(parts) != requiredCoreSegments {
		return coreVersion{}, fmt.Errorf("expected %d segments in %q", requiredCoreSegments, raw)
	}

	maj, err := parseSegment(parts[0], "major", raw)
	if err != nil {
		return coreVersion{}, err
	}
	min, err := parseSegment(parts[1], "minor", raw)
	if err != nil {
		return coreVersion{}, err
	}
	patch, err := parseSegment(parts[2], "patch", raw)
	if err != nil {
		return coreVersion{}, err
	}
	return coreVersion{major: maj, minor: min, patch: patch}, nil
}

// parseSegment parses an individual non-negative numeric segment as uint64.
func parseSegment(segment, segmentName, raw string) (uint64, error) {
	val, err := strconv.ParseUint(segment, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s version in %q: %w", segmentName, raw, err)
	}
	return val, nil
}

// extractPreRelease splits a non-empty prerelease tag into dot-separated identifiers.
func extractPreRelease(prerelease string) []string {
	if prerelease == "" {
		return nil
	}
	clean := strings.TrimPrefix(prerelease, prereleaseDelimiter)
	return strings.Split(clean, segmentDelimiter)
}
