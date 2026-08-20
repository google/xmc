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

// Parse parses a semantic version string conforming to SemVer 2.0.0 with optional leading 'v' or 'V'.
func Parse(v string) (*Version, error) {
	raw := v
	s := strings.TrimSpace(v)
	if s == "" {
		return nil, errors.New("empty version string")
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		s = s[1:]
	}

	var build string
	if idx := strings.IndexByte(s, '+'); idx != -1 {
		build = s[idx+1:]
		s = s[:idx]
		if err := validateBuild(build); err != nil {
			return nil, fmt.Errorf("invalid build metadata %q: %w", build, err)
		}
	}

	var preRelease []string
	if idx := strings.IndexByte(s, '-'); idx != -1 {
		preStr := s[idx+1:]
		s = s[:idx]
		var err error
		preRelease, err = parsePreRelease(preStr)
		if err != nil {
			return nil, fmt.Errorf("invalid pre-release identifier %q: %w", preStr, err)
		}
	}

	core, err := parseCore(s)
	if err != nil {
		return nil, fmt.Errorf("invalid core version %q: %w", s, err)
	}

	return &Version{
		Major:      core.major,
		Minor:      core.minor,
		Patch:      core.patch,
		PreRelease: preRelease,
		Build:      build,
		Raw:        raw,
	}, nil
}

type coreVersion struct {
	major uint64
	minor uint64
	patch uint64
}

// parseCore parses the Major.Minor.Patch segment of a SemVer string.
func parseCore(s string) (*coreVersion, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 numeric segments, got %d", len(parts))
	}
	maj, err := parseNumericSegment(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid major version: %w", err)
	}
	min, err := parseNumericSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid minor version: %w", err)
	}
	patch, err := parseNumericSegment(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid patch version: %w", err)
	}
	return &coreVersion{major: maj, minor: min, patch: patch}, nil
}

// parseNumericSegment parses an individual non-negative integer segment and validates no leading zeroes.
func parseNumericSegment(seg string) (uint64, error) {
	if seg == "" {
		return 0, errors.New("empty segment")
	}
	if !isNumeric(seg) {
		return 0, errors.New("segment contains non-digit characters")
	}
	if len(seg) > 1 && seg[0] == '0' {
		return 0, errors.New("leading zero is not permitted")
	}
	val, err := strconv.ParseUint(seg, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse uint64: %w", err)
	}
	return val, nil
}

// parsePreRelease parses and validates dot-separated pre-release identifiers.
func parsePreRelease(s string) ([]string, error) {
	if s == "" {
		return nil, errors.New("empty pre-release string")
	}
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if part == "" {
			return nil, errors.New("empty pre-release identifier segment")
		}
		if !isValidIdentifier(part) {
			return nil, fmt.Errorf("identifier %q contains invalid characters", part)
		}
		if isNumeric(part) && len(part) > 1 && part[0] == '0' {
			return nil, fmt.Errorf("numeric identifier %q has leading zero", part)
		}
	}
	return parts, nil
}

// validateBuild validates dot-separated build metadata identifiers.
func validateBuild(s string) error {
	if s == "" {
		return errors.New("empty build metadata")
	}
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if part == "" {
			return errors.New("empty build metadata segment")
		}
		if !isValidIdentifier(part) {
			return fmt.Errorf("identifier %q contains invalid characters", part)
		}
	}
	return nil
}

// isValidIdentifier returns true if s consists only of ASCII alphanumerics and hyphens.
func isValidIdentifier(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// isNumeric returns true if s consists exclusively of ASCII digits.
func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Compare compares two version strings according to SemVer 2.0.0 §11.
// Returns -1 if v1 < v2, 0 if v1 == v2, and 1 if v1 > v2.
func Compare(v1, v2 string) (int, error) {
	ver1, err := Parse(v1)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %q: %w", v1, err)
	}
	ver2, err := Parse(v2)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %q: %w", v2, err)
	}
	return ver1.Compare(ver2), nil
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
	return comparePreRelease(v.PreRelease, other.PreRelease)
}

// comparePreRelease compares pre-release identifier slices per SemVer 2.0.0 §11.4.4.
func comparePreRelease(p1, p2 []string) int {
	if len(p1) == 0 && len(p2) == 0 {
		return 0
	}
	if len(p1) == 0 {
		return 1
	}
	if len(p2) == 0 {
		return -1
	}
	minLen := len(p1)
	if len(p2) < minLen {
		minLen = len(p2)
	}
	for i := 0; i < minLen; i++ {
		if cmp := compareSingleIdentifier(p1[i], p2[i]); cmp != 0 {
			return cmp
		}
	}
	return compareSliceLengths(len(p1), len(p2))
}

// compareSingleIdentifier compares two pre-release dot-separated identifiers per §11.4.4.
func compareSingleIdentifier(id1, id2 string) int {
	if id1 == id2 {
		return 0
	}
	isNum1 := isNumeric(id1)
	isNum2 := isNumeric(id2)
	if isNum1 && isNum2 {
		if len(id1) < len(id2) {
			return -1
		}
		if len(id1) > len(id2) {
			return 1
		}
		if id1 < id2 {
			return -1
		}
		return 1
	}
	if isNum1 {
		return -1
	}
	if isNum2 {
		return 1
	}
	if id1 < id2 {
		return -1
	}
	return 1
}

// compareSliceLengths returns -1, 0, or 1 based on relative lengths of pre-release slices.
func compareSliceLengths(len1, len2 int) int {
	if len1 < len2 {
		return -1
	}
	if len1 > len2 {
		return 1
	}
	return 0
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
	curVer, err := Parse(current)
	if err != nil {
		return false, fmt.Errorf("invalid current version %q: %w", current, err)
	}
	targetVer, err := Parse(target)
	if err != nil {
		return false, fmt.Errorf("invalid target version %q: %w", target, err)
	}
	if targetVer.Major != curVer.Major || targetVer.Minor != curVer.Minor {
		return false, nil
	}
	if len(targetVer.PreRelease) > 0 && len(curVer.PreRelease) == 0 {
		return false, nil
	}
	return targetVer.GreaterThan(curVer), nil
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
