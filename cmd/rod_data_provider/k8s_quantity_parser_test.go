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

package main

import (
	"testing"
)

func TestParseK8sResourceVal(t *testing.T) {
	tests := []struct {
		input    string
		wantVal  float64
		wantUnit string
	}{
		{input: "8", wantVal: 8.0, wantUnit: ""},
		{input: "8Gi", wantVal: 8.0, wantUnit: "Gi"},
		{input: "1024Mi", wantVal: 1.0, wantUnit: "Mi"},
		{input: "2000m", wantVal: 2.0, wantUnit: "m"},
		{input: "", wantVal: 0.0, wantUnit: ""},
		{input: "-", wantVal: 0.0, wantUnit: ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotVal, gotUnit := parseK8sResourceVal(tt.input)
			if gotVal != tt.wantVal || gotUnit != tt.wantUnit {
				t.Errorf("parseK8sResourceVal(%q) = (%v, %q), want (%v, %q)", tt.input, gotVal, gotUnit, tt.wantVal, tt.wantUnit)
			}
		})
	}
}
