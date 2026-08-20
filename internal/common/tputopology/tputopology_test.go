// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tputopology

import (
	"errors"
	"testing"
)

func TestParseTotalChips(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "empty_string", input: "", want: 1, wantErr: true},
		{name: "spaces_only", input: "   ", want: 1, wantErr: true},
		{name: "suffix_only", input: ":2", want: 1, wantErr: true},
		{name: "numeric_zero", input: "0", want: 1, wantErr: true},
		{name: "topology_3d", input: "2x2x4", want: 16, wantErr: false},
		{name: "topology_3d_uppercase", input: "2X2X4", want: 16, wantErr: false},
		{name: "topology_3d_whitespace", input: " 2x2x4 ", want: 16, wantErr: false},
		{name: "topology_2d", input: "4x4", want: 16, wantErr: false},
		{name: "topology_with_suffix", input: "2x2x4:2", want: 16, wantErr: false},
		{name: "topology_with_modifier_suffix", input: "4x4:custom", want: 16, wantErr: false},
		{name: "slice_v5p", input: "v5p-8", want: 8, wantErr: false},
		{name: "slice_v4", input: "v4-16", want: 16, wantErr: false},
		{name: "slice_v5e", input: "v5e-4", want: 4, wantErr: false},
		{name: "slice_v6e", input: "v6e-8", want: 8, wantErr: false},
		{name: "slice_flex_with_x", input: "flex-16", want: 16, wantErr: false},
		{name: "slice_v5x_with_x", input: "v5x-8", want: 8, wantErr: false},
		{name: "slice_max_with_x", input: "max-32", want: 32, wantErr: false},
		{name: "slice_with_suffix", input: "v5p-8:2", want: 8, wantErr: false},
		{name: "numeric_string", input: "8", want: 8, wantErr: false},
		{name: "numeric_one", input: "1", want: 1, wantErr: false},
		{name: "invalid_string", input: "invalid", want: 1, wantErr: true},
		{name: "zero_dimension", input: "2x0", want: 1, wantErr: true},
		{name: "negative_dimension", input: "2x-2", want: 1, wantErr: true},
		{name: "invalid_dimension", input: "2x2xX", want: 1, wantErr: true},
		{name: "multiplication_overflow", input: "9223372036854775807x2", want: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChips := ParseTotalChips(tt.input)
			if gotChips != tt.want {
				t.Errorf("ParseTotalChips(%q) = %d, want %d", tt.input, gotChips, tt.want)
			}

			parsed, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) expected error, got nil", tt.input)
				} else if !errors.Is(err, ErrInvalidTopology) {
					t.Errorf("Parse(%q) error = %v, want errors.Is ErrInvalidTopology", tt.input, err)
				}
				if parsed != 0 {
					t.Errorf("Parse(%q) on error returned %d, want 0", tt.input, parsed)
				}
			} else {
				if err != nil {
					t.Errorf("Parse(%q) unexpected error = %v", tt.input, err)
				}
				if parsed != tt.want {
					t.Errorf("Parse(%q) = %d, want %d", tt.input, parsed, tt.want)
				}
			}
		})
	}
}
