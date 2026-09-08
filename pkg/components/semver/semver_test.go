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

package semver_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/components/semver"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *semver.Version
		wantErr bool
	}{
		{
			name:  "standard major.minor.patch",
			input: "1.2.3",
			want: &semver.Version{
				Major: 1,
				Minor: 2,
				Patch: 3,
				Raw:   "1.2.3",
			},
		},
		{
			name:  "leading lowercase v",
			input: "v1.2.3",
			want: &semver.Version{
				Major: 1,
				Minor: 2,
				Patch: 3,
				Raw:   "v1.2.3",
			},
		},
		{
			name:  "leading uppercase V",
			input: "V2.0.0",
			want: &semver.Version{
				Major: 2,
				Minor: 0,
				Patch: 0,
				Raw:   "V2.0.0",
			},
		},
		{
			name:  "prerelease single identifier",
			input: "1.0.0-alpha",
			want: &semver.Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				PreRelease: []string{"alpha"},
				Raw:        "1.0.0-alpha",
			},
		},
		{
			name:  "prerelease dot separated alphanumeric and numeric",
			input: "1.0.0-alpha.1.beta.0",
			want: &semver.Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				PreRelease: []string{"alpha", "1", "beta", "0"},
				Raw:        "1.0.0-alpha.1.beta.0",
			},
		},
		{
			name:  "build metadata only",
			input: "1.0.0+20130313144700",
			want: &semver.Version{
				Major: 1,
				Minor: 0,
				Patch: 0,
				Build: "20130313144700",
				Raw:   "1.0.0+20130313144700",
			},
		},
		{
			name:  "prerelease and build metadata",
			input: "1.0.0-beta.11+exp.sha.5114f85",
			want: &semver.Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				PreRelease: []string{"beta", "11"},
				Build:      "exp.sha.5114f85",
				Raw:        "1.0.0-beta.11+exp.sha.5114f85",
			},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "incomplete segments 1.0",
			input:   "1.0",
			wantErr: true,
		},
		{
			name:    "too many core segments",
			input:   "1.0.0.1",
			wantErr: true,
		},
		{
			name:    "leading zero in major",
			input:   "01.0.0",
			wantErr: true,
		},
		{
			name:    "leading zero in minor",
			input:   "1.01.0",
			wantErr: true,
		},
		{
			name:    "leading zero in patch",
			input:   "1.0.01",
			wantErr: true,
		},
		{
			name:    "leading zero in numeric prerelease",
			input:   "1.0.0-01",
			wantErr: true,
		},
		{
			name:    "double leading v prefix",
			input:   "vv1.0.0",
			wantErr: true,
		},
		{
			name:    "leading plus in major",
			input:   "+1.0.0",
			wantErr: true,
		},
		{
			name:    "leading plus in minor",
			input:   "1.+0.0",
			wantErr: true,
		},
		{
			name:    "leading plus in patch",
			input:   "1.0.+1",
			wantErr: true,
		},
		{
			name:    "mixed double leading v prefix",
			input:   "vV1.0.0",
			wantErr: true,
		},
		{
			name:    "empty prerelease segment",
			input:   "1.0.0-alpha..1",
			wantErr: true,
		},
		{
			name:    "empty build segment",
			input:   "1.0.0+build..1",
			wantErr: true,
		},
		{
			name:    "invalid character in prerelease",
			input:   "1.0.0-alpha@test",
			wantErr: true,
		},
		{
			name:    "invalid character in build metadata",
			input:   "1.0.0+build#1",
			wantErr: true,
		},
		{
			name:    "empty build string after plus",
			input:   "1.0.0+",
			wantErr: true,
		},
		{
			name:    "empty prerelease string after dash",
			input:   "1.0.0-",
			wantErr: true,
		},
		{
			name:    "empty segment in core version",
			input:   "1..0",
			wantErr: true,
		},
		{
			name:    "uint64 overflow in major segment",
			input:   "99999999999999999999999999999999.0.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := semver.Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse(%q) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestSemverComparePrecedence(t *testing.T) {
	orderedVersions := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
		"2.1.0",
		"2.1.1",
	}

	for i := 0; i < len(orderedVersions)-1; i++ {
		v1 := orderedVersions[i]
		v2 := orderedVersions[i+1]

		cmpVal, err := semver.Compare(v1, v2)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", v1, v2, err)
		}
		if cmpVal >= 0 {
			t.Errorf("expected %q < %q, got cmp = %d", v1, v2, cmpVal)
		}

		cmpRev, err := semver.Compare(v2, v1)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", v2, v1, err)
		}
		if cmpRev <= 0 {
			t.Errorf("expected %q > %q, got cmp = %d", v2, v1, cmpRev)
		}
	}
}

func TestSemverCompareBuildIgnored(t *testing.T) {
	v1 := "1.0.0+build.1"
	v2 := "1.0.0+build.2"
	cmpVal, err := semver.Compare(v1, v2)
	if err != nil {
		t.Fatalf("Compare(%q, %q) unexpected error: %v", v1, v2, err)
	}
	if cmpVal != 0 {
		t.Errorf("Compare(%q, %q) = %d, want 0 (build metadata must be ignored)", v1, v2, cmpVal)
	}
}

func TestSemverComparePrecedenceValues(t *testing.T) {
	tests := []struct {
		name string
		v1   string
		v2   string
		want int
	}{
		{name: "equal versions", v1: "1.0.0", v2: "1.0.0", want: 0},
		{name: "equal with v prefix", v1: "v1.0.0", v2: "1.0.0", want: 0},
		{name: "less major", v1: "1.0.0", v2: "2.0.0", want: -1},
		{name: "greater minor", v1: "1.2.0", v2: "1.1.0", want: 1},
		{name: "prerelease less than normal", v1: "1.0.0-alpha", v2: "1.0.0", want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmpVal, err := semver.Compare(tt.v1, tt.v2)
			if err != nil {
				t.Fatalf("unexpected error comparing %q and %q: %v", tt.v1, tt.v2, err)
			}
			if cmpVal != tt.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tt.v1, tt.v2, cmpVal, tt.want)
			}
		})
	}
}

func TestVersionStructMethods(t *testing.T) {
	v1, err := semver.Parse("1.2.3-alpha.1+build.01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v2, err := semver.Parse("1.2.3-alpha.2+build.02")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vEqual, err := semver.Parse("1.2.3-alpha.1+otherbuild")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !v1.LessThan(v2) {
		t.Errorf("expected %v.LessThan(%v) to be true", v1, v2)
	}
	if !v2.GreaterThan(v1) {
		t.Errorf("expected %v.GreaterThan(%v) to be true", v2, v1)
	}
	if !v1.Equal(vEqual) {
		t.Errorf("expected %v.Equal(%v) to be true ignoring build metadata", v1, vEqual)
	}
	if v1.Compare(nil) != 1 {
		t.Errorf("expected non-nil Version.Compare(nil) = 1")
	}
	var vNil *semver.Version
	if vNil.Compare(v1) != -1 {
		t.Errorf("expected nil.Compare(non-nil) = -1")
	}
	if vNil.Compare(nil) != 0 {
		t.Errorf("expected nil.Compare(nil) = 0")
	}
	if vNil.String() != "" {
		t.Errorf("expected nil Version.String() = \"\", got %q", vNil.String())
	}
}

func TestIsPatchUpdate(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    bool
		wantErr bool
	}{
		{name: "standard patch update", current: "1.2.3", target: "1.2.4", want: true},
		{name: "multiple patch steps", current: "1.2.3", target: "1.2.10", want: true},
		{name: "same version", current: "1.2.3", target: "1.2.3", want: false},
		{name: "patch downgrade", current: "1.2.4", target: "1.2.3", want: false},
		{name: "minor bump is not patch update", current: "1.2.3", target: "1.3.0", want: false},
		{name: "major bump is not patch update", current: "1.2.3", target: "2.0.0", want: false},
		{name: "stable to prerelease is not patch update", current: "1.2.3", target: "1.2.4-rc.1", want: false},
		{name: "prerelease to stable patch is allowed", current: "1.2.3-rc.1", target: "1.2.3", want: true},
		{name: "invalid current version", current: "invalid", target: "1.2.3", wantErr: true},
		{name: "invalid target version", current: "1.2.3", target: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := semver.IsPatchUpdate(tt.current, tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsPatchUpdate(%q, %q) err = %v, wantErr = %v", tt.current, tt.target, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("IsPatchUpdate(%q, %q) = %v, want %v", tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestSatisfiesMinVersion(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		minVersion string
		want       bool
		wantErr    bool
	}{
		{name: "higher patch satisfies", version: "1.2.4", minVersion: "1.2.3", want: true},
		{name: "exact version satisfies", version: "1.2.3", minVersion: "1.2.3", want: true},
		{name: "lower patch fails", version: "1.2.2", minVersion: "1.2.3", want: false},
		{name: "higher minor satisfies", version: "1.3.0", minVersion: "1.2.3", want: true},
		{name: "higher major satisfies", version: "2.0.0", minVersion: "1.2.3", want: true},
		{name: "with v prefix satisfies", version: "v1.2.3", minVersion: "1.2.3", want: true},
		{name: "invalid version string", version: "not-semver", minVersion: "1.0.0", wantErr: true},
		{name: "invalid minVersion string", version: "1.0.0", minVersion: "not-semver", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := semver.SatisfiesMinVersion(tt.version, tt.minVersion)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SatisfiesMinVersion(%q, %q) err = %v, wantErr = %v", tt.version, tt.minVersion, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SatisfiesMinVersion(%q, %q) = %v, want %v", tt.version, tt.minVersion, got, tt.want)
			}
		})
	}
}
