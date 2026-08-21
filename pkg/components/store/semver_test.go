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

package store_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"xmanager_cloud/pkg/components/store"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *store.Version
		wantErr bool
	}{
		{
			name:  "standard major.minor.patch",
			input: "1.2.3",
			want: &store.Version{
				Major: 1,
				Minor: 2,
				Patch: 3,
				Raw:   "1.2.3",
			},
		},
		{
			name:  "leading lowercase v",
			input: "v1.2.3",
			want: &store.Version{
				Major: 1,
				Minor: 2,
				Patch: 3,
				Raw:   "v1.2.3",
			},
		},
		{
			name:  "leading uppercase V",
			input: "V2.0.0",
			want: &store.Version{
				Major: 2,
				Minor: 0,
				Patch: 0,
				Raw:   "V2.0.0",
			},
		},
		{
			name:  "prerelease single identifier",
			input: "1.0.0-alpha",
			want: &store.Version{
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
			want: &store.Version{
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
			want: &store.Version{
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
			want: &store.Version{
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
			got, err := store.Parse(tt.input)
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
	// Precedence order defined in SemVer 2.0.0 §11.4
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

		cmpVal, err := store.Compare(v1, v2)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", v1, v2, err)
		}
		if cmpVal != -1 {
			t.Errorf("Expected %q < %q, got cmp = %d", v1, v2, cmpVal)
		}

		// Reverse comparison
		cmpRev, err := store.Compare(v2, v1)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", v2, v1, err)
		}
		if cmpRev != 1 {
			t.Errorf("Expected %q > %q, got cmp = %d", v2, v1, cmpRev)
		}
	}
}

func TestCompareBuildMetadataIgnored(t *testing.T) {
	v1 := "1.0.0+build1"
	v2 := "1.0.0+build2"

	cmpVal, err := store.Compare(v1, v2)
	if err != nil {
		t.Fatalf("Compare(%q, %q) unexpected error: %v", v1, v2, err)
	}
	if cmpVal != 0 {
		t.Errorf("Compare(%q, %q) = %d, want 0 (build metadata must be ignored)", v1, v2, cmpVal)
	}
}

func TestNumericVsAlphanumericPreRelease(t *testing.T) {
	// Numeric identifiers always have lower precedence than non-numeric identifiers (1 < alpha)
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{v1: "1.0.0-1", v2: "1.0.0-alpha", want: -1},
		{v1: "1.0.0-100000000000000000000", v2: "1.0.0-0alpha", want: -1},
		{v1: "1.0.0-100000000000000000000", v2: "1.0.0-200000000000000000000", want: -1},
	}

	for _, tt := range tests {
		cmpVal, err := store.Compare(tt.v1, tt.v2)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", tt.v1, tt.v2, err)
		}
		if cmpVal != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.v1, tt.v2, cmpVal, tt.want)
		}
	}
}

func TestCompareErrors(t *testing.T) {
	if _, err := store.Compare("invalid", "1.0.0"); err == nil {
		t.Errorf("Compare() expected error for invalid v1, got nil")
	}
	if _, err := store.Compare("1.0.0", "invalid"); err == nil {
		t.Errorf("Compare() expected error for invalid v2, got nil")
	}
}

func TestVersionMethods(t *testing.T) {
	v1, err := store.Parse("1.2.3-alpha.1+build.01")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v2, err := store.Parse("1.2.3-alpha.2+build.02")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	vEqual, err := store.Parse("1.2.3-alpha.1+otherbuild")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if !v1.LessThan(v2) {
		t.Errorf("%v should be LessThan %v", v1, v2)
	}
	if !v2.GreaterThan(v1) {
		t.Errorf("%v should be GreaterThan %v", v2, v1)
	}
	if !v1.Equal(vEqual) {
		t.Errorf("%v should Equal %v", v1, vEqual)
	}
	if v1.Equal(v2) {
		t.Errorf("%v should not Equal %v", v1, v2)
	}

	// Test nil receiver comparisons
	var nilV *store.Version
	if nilV.Compare(nilV) != 0 {
		t.Errorf("nil.Compare(nil) = %d, want 0", nilV.Compare(nilV))
	}
	if nilV.Compare(v1) != -1 {
		t.Errorf("nil.Compare(v1) = %d, want -1", nilV.Compare(v1))
	}
	if v1.Compare(nilV) != 1 {
		t.Errorf("v1.Compare(nil) = %d, want 1", v1.Compare(nilV))
	}
	if nilV.String() != "" {
		t.Errorf("nil.String() = %q, want empty string", nilV.String())
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "1.0.0", want: "1.0.0"},
		{input: "v1.2.3", want: "1.2.3"},
		{input: "1.2.3-alpha.1", want: "1.2.3-alpha.1"},
		{input: "1.2.3+build.42", want: "1.2.3+build.42"},
		{input: "1.2.3-beta.2+build.99", want: "1.2.3-beta.2+build.99"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v, err := store.Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			if got := v.String(); got != tt.want {
				t.Errorf("Version.String() = %q, want %q", got, tt.want)
			}
		})
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
		{name: "strictly higher patch", current: "1.2.0", target: "1.2.1", want: true},
		{name: "same version", current: "1.2.0", target: "1.2.0", want: false},
		{name: "lower patch version", current: "1.2.2", target: "1.2.1", want: false},
		{name: "different minor version", current: "1.2.0", target: "1.3.0", want: false},
		{name: "different major version", current: "1.2.0", target: "2.2.0", want: false},
		{name: "stable to higher prerelease is not patch update", current: "1.2.0", target: "1.2.1-alpha.1", want: false},
		{name: "prerelease to final patch", current: "1.2.0-rc.1", target: "1.2.0", want: true},
		{name: "invalid current version", current: "invalid", target: "1.2.1", wantErr: true},
		{name: "invalid target version", current: "1.2.0", target: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.IsPatchUpdate(tt.current, tt.target)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsPatchUpdate(%q, %q) err = %v, wantErr = %v", tt.current, tt.target, err, tt.wantErr)
			}
			if tt.wantErr {
				return
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
		{name: "higher version satisfies", version: "1.5.0", minVersion: "1.2.0", want: true},
		{name: "exact version satisfies", version: "1.2.0", minVersion: "1.2.0", want: true},
		{name: "lower version does not satisfy", version: "1.1.9", minVersion: "1.2.0", want: false},
		{name: "prerelease does not satisfy final", version: "1.2.0-alpha", minVersion: "1.2.0", want: false},
		{name: "with v prefix", version: "v2.0.0", minVersion: "v1.9.9", want: true},
		{name: "invalid version", version: "invalid", minVersion: "1.0.0", wantErr: true},
		{name: "invalid minVersion", version: "1.0.0", minVersion: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.SatisfiesMinVersion(tt.version, tt.minVersion)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SatisfiesMinVersion(%q, %q) err = %v, wantErr = %v", tt.version, tt.minVersion, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("SatisfiesMinVersion(%q, %q) = %v, want %v", tt.version, tt.minVersion, got, tt.want)
			}
		})
	}
}
