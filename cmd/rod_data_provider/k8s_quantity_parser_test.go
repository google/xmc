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
		resName string
		input   string
		wantVal float64
	}{
		{resName: "nvidia.com/gpu", input: "8", wantVal: 8.0},
		{resName: "memory", input: "8Gi", wantVal: 8.0},
		{resName: "memory", input: "1024Mi", wantVal: 1.0},
		{resName: "memory", input: "1048576Ki", wantVal: 1.0},
		{resName: "cpu", input: "500m", wantVal: 0.5},
		{resName: "cpu", input: "2000m", wantVal: 2.0},
		{resName: "memory", input: "", wantVal: 0.0},
		{resName: "cpu", input: "-", wantVal: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.resName+"_"+tt.input, func(t *testing.T) {
			gotVal := parseK8sResourceVal(tt.resName, tt.input)
			if gotVal != tt.wantVal {
				t.Errorf("parseK8sResourceVal(%q, %q) = %v, want %v", tt.resName, tt.input, gotVal, tt.wantVal)
			}
		})
	}
}
