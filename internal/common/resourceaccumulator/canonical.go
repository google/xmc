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
	"strings"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

// BytesPerGiB is the binary gibibyte divisor used across ROD memory metrics.
const BytesPerGiB = 1024 * 1024 * 1024

// FormatCompoundAccelerator combines a base resource name with a specific accelerator model.
func FormatCompoundAccelerator(resourceName, model string) string {
	if model == "" || model == resourceName {
		return resourceName
	}
	if strings.HasPrefix(model, resourceName+": ") || strings.HasPrefix(model, resourceName+"/") {
		return model
	}
	return resourceName + ": " + model
}

// canonicalizeResourceType normalizes an Accumulator key (already lowercase) into a canonical ROD
// resource type (e.g. "nvidia.com/gpu: nvidia-tesla-h100", "google.com/tpu: tpu-v5-lite-podslice").
func canonicalizeResourceType(key string) string {
	gpu, tpu := constants.NvidiaGPUResourceName, constants.TPUResourceName
	switch {
	case key == ResourceTypeCPU || key == ResourceTypeMemory:
		return key
	case strings.HasPrefix(key, gpu+": ") || strings.HasPrefix(key, tpu+": "):
		return key
	case IsGenericGPU(key):
		return gpu
	case IsGenericTPU(key):
		return tpu
	}
	if mapped, ok := constants.GPUTypeToGKEAccelerator[strings.ToUpper(key)]; ok {
		return FormatCompoundAccelerator(gpu, mapped)
	}
	if IsSpecificTPUKey(key) {
		return FormatCompoundAccelerator(tpu, key)
	}
	if IsSpecificGPUKey(key) {
		return FormatCompoundAccelerator(gpu, key)
	}
	return key
}

// NormalizeUnits converts a raw resource quantity into ROD display units: memory and storage go
// from bytes to GiB, everything else (cores, devices, chips) is unchanged.
func NormalizeUnits(resourceType string, value float64) float64 {
	if IsMemoryOrStorageResource(resourceType) {
		return value / BytesPerGiB
	}
	return value
}

// ExtractNormalizedExecutableResources extracts requested resources for an Executable with
// canonical accelerator type names and memory/storage normalized from bytes to GiB.
func ExtractNormalizedExecutableResources(exec *essmsgpb.Executable) map[string]float64 {
	acc := New()
	acc.AccumulateExecutable(exec)
	normalized := make(map[string]float64)
	for key, val := range acc.Totals() {
		canonical := canonicalizeResourceType(key)
		normalized[canonical] += NormalizeUnits(canonical, val)
	}
	return normalized
}
