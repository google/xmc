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

package resourceaccumulator

import (
	"testing"
)

func TestIsSpecificAcceleratorName_Classifier(t *testing.T) {
	tests := []struct {
		name       string
		flavorName string
		want       bool
	}{
		// Valid GPU accelerators
		{name: "nvidia-tesla-h100", flavorName: "nvidia-tesla-h100", want: true},
		{name: "nvidia-l4", flavorName: "nvidia-l4", want: true},
		{name: "nvidia-t4", flavorName: "nvidia-t4", want: true},
		{name: "nvidia-a100", flavorName: "nvidia-a100", want: true},
		{name: "gpack-gpu", flavorName: "gpack-gpu", want: true},

		// Valid TPU accelerators
		{name: "tpu-v4", flavorName: "tpu-v4", want: true},
		{name: "tpu-v5p", flavorName: "tpu-v5p", want: true},
		{name: "tpu-v5", flavorName: "tpu-v5", want: true},
		{name: "v5e-8", flavorName: "v5e-8", want: true},
		{name: "v4-8", flavorName: "v4-8", want: true},
		{name: "v5p-8", flavorName: "v5p-8", want: true},

		// Non-accelerator words with embedded short keywords (false positive guard)
		{name: "pool4", flavorName: "pool4", want: false},
		{name: "dev4", flavorName: "dev4", want: false},
		{name: "level4", flavorName: "level4", want: false},
		{name: "step4", flavorName: "step4", want: false},
		{name: "server6", flavorName: "server6", want: false},
		{name: "http4", flavorName: "http4", want: false},
		{name: "backup4", flavorName: "backup4", want: false},
		{name: "env-v5", flavorName: "env-v5", want: false},
		{name: "dev-v4", flavorName: "dev-v4", want: false},

		// Generic flavors and resources
		{name: "empty string", flavorName: "", want: false},
		{name: "generic-gpu", flavorName: "generic-gpu", want: false},
		{name: "standard-gpu", flavorName: "standard-gpu", want: false},
		{name: "fallback", flavorName: "fallback", want: false},
		{name: "spot", flavorName: "spot", want: false},
		{name: "ondemand", flavorName: "ondemand", want: false},
		{name: "default", flavorName: "default", want: false},
		{name: "shared", flavorName: "shared", want: false},
		{name: "custom", flavorName: "custom", want: false},
		{name: "gpu-flavor", flavorName: "gpu-flavor", want: false},
		{name: "tpu-flavor", flavorName: "tpu-flavor", want: false},

		// TPU Topology geometries
		{name: "2x2x1 topology", flavorName: "2x2x1", want: false},
		{name: "2x4 topology", flavorName: "2x4", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSpecificAcceleratorName(tt.flavorName)
			if got != tt.want {
				t.Errorf("IsSpecificAcceleratorName(%q) = %v, want %v", tt.flavorName, got, tt.want)
			}
		})
	}
}

func TestIsAcceleratorResource(t *testing.T) {
	tests := []struct {
		resName string
		want    bool
	}{
		{resName: "nvidia.com/gpu", want: true},
		{resName: "gpu", want: true},
		{resName: "google.com/tpu", want: true},
		{resName: "tpu", want: true},
		{resName: "accelerator", want: true},
		{resName: "cpu", want: false},
		{resName: "memory", want: false},
		{resName: "storage", want: false},
		{resName: "ephemeral-storage", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.resName, func(t *testing.T) {
			got := IsAcceleratorResource(tt.resName)
			if got != tt.want {
				t.Errorf("IsAcceleratorResource(%q) = %v, want %v", tt.resName, got, tt.want)
			}
		})
	}
}

func TestIsMemoryOrStorageResource(t *testing.T) {
	tests := []struct {
		resType string
		want    bool
	}{
		{resType: "memory", want: true},
		{resType: "Memory", want: true},
		{resType: "storage", want: true},
		{resType: "ephemeral-storage", want: true},
		{resType: "cpu", want: false},
		{resType: "gpu", want: false},
		{resType: "tpu", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.resType, func(t *testing.T) {
			got := IsMemoryOrStorageResource(tt.resType)
			if got != tt.want {
				t.Errorf("IsMemoryOrStorageResource(%q) = %v, want %v", tt.resType, got, tt.want)
			}
		})
	}
}

func TestIsGenericGPUAndTPU(t *testing.T) {
	if !IsGenericGPU("gpu") || !IsGenericGPU("nvidia.com/gpu") {
		t.Errorf("IsGenericGPU failed for standard generic GPU keys")
	}
	if IsGenericGPU("nvidia-tesla-h100") || IsGenericGPU("nvidia-l4") {
		t.Errorf("IsGenericGPU returned true for specific GPU models")
	}

	if !IsGenericTPU("tpu") || !IsGenericTPU("google.com/tpu") {
		t.Errorf("IsGenericTPU failed for standard generic TPU keys")
	}
	if IsGenericTPU("tpu-v4") || IsGenericTPU("v5p-8") {
		t.Errorf("IsGenericTPU returned true for specific TPU models")
	}
}

func TestMatchesShortAcceleratorKeyword(t *testing.T) {
	tests := []struct {
		str  string
		kw   string
		want bool
	}{
		{str: "l4", kw: "l4", want: true},
		{str: "nvidia-l4", kw: "l4", want: true},
		{str: "l4-gpu", kw: "l4", want: true},
		{str: "tpu-v4", kw: "v4", want: true},
		{str: "v4-8", kw: "v4", want: true},
		{str: "pool4", kw: "l4", want: false},
		{str: "level4", kw: "l4", want: false},
		{str: "server6", kw: "v6", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.str+"_"+tt.kw, func(t *testing.T) {
			got := MatchesShortAcceleratorKeyword(tt.str, tt.kw)
			if got != tt.want {
				t.Errorf("MatchesShortAcceleratorKeyword(%q, %q) = %v, want %v", tt.str, tt.kw, got, tt.want)
			}
		})
	}
}
