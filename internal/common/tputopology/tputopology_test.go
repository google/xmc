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
			got := ParseTotalChips(tt.input)
			if got != tt.want {
				t.Errorf("ParseTotalChips(%q) = %d, want %d", tt.input, got, tt.want)
			}

			parsed, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && parsed != tt.want {
				t.Errorf("Parse(%q) = %d, want %d", tt.input, parsed, tt.want)
			}
		})
	}
}
