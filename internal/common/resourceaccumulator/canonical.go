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
)

// BytesPerGiB is the binary gibibyte divisor used across ROD memory metrics.
const BytesPerGiB = 1024 * 1024 * 1024

const (
	// kubernetesGPUResource is the Kubernetes extended resource name for NVIDIA GPUs.
	kubernetesGPUResource = "nvidia.com/gpu"
	// kubernetesTPUResource is the Kubernetes extended resource name for Cloud TPUs.
	kubernetesTPUResource = "google.com/tpu"
)

var symbolicGPUToAccelerator = map[string]string{
	"p4":         "nvidia-tesla-p4",
	"t4":         "nvidia-tesla-t4",
	"l4_24th":    "nvidia-l4",
	"p100":       "nvidia-tesla-p100",
	"v100":       "nvidia-tesla-v100",
	"a100":       "nvidia-a100-40gb",
	"a100_80gib": "nvidia-a100-80gb",
	"h100":       "nvidia-h100-80gb",
	"h100_mega":  "nvidia-h100-mega-80gb",
	"h200":       "nvidia-h200-141gb",
	"b200":       "nvidia-b200",
	"gb200":      "nvidia-gb200",
	"b40_48th":   "nvidia-rtx-pro-6000",
}

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

// canonicalizeResourceType normalizes raw resource identifiers into canonical ROD resource types
// (e.g. "nvidia.com/gpu: nvidia-tesla-h100", "google.com/tpu: tpu-v5-lite-podslice", "cpu", "memory").
func canonicalizeResourceType(rawType string) string {
	lower := strings.ToLower(strings.TrimSpace(rawType))
	if lower == "" || lower == ResourceTypeCPU || lower == ResourceTypeMemory {
		return lower
	}
	if strings.HasPrefix(lower, kubernetesGPUResource+": ") || strings.HasPrefix(lower, kubernetesTPUResource+": ") {
		return lower
	}
	if IsGenericGPU(lower) {
		return kubernetesGPUResource
	}
	if IsGenericTPU(lower) {
		return kubernetesTPUResource
	}
	if mapped, ok := symbolicGPUToAccelerator[lower]; ok {
		return FormatCompoundAccelerator(kubernetesGPUResource, mapped)
	}
	if IsSpecificTPUKey(lower) {
		return FormatCompoundAccelerator(kubernetesTPUResource, lower)
	}
	if IsSpecificGPUKey(lower) {
		return FormatCompoundAccelerator(kubernetesGPUResource, lower)
	}
	return lower
}

// ExtractNormalizedExecutableResources extracts requested resources for an Executable with
// canonical accelerator type names and memory/storage normalized from bytes to GiB.
func ExtractNormalizedExecutableResources(exec *essmsgpb.Executable) map[string]float64 {
	if exec == nil {
		return nil
	}
	acc := New()
	acc.AccumulateExecutable(exec)
	rawTotals := acc.Totals()
	normalized := make(map[string]float64, len(rawTotals))
	for rawType, val := range rawTotals {
		if val <= 0 {
			continue
		}
		canonical := canonicalizeResourceType(rawType)
		if IsMemoryOrStorageResource(canonical) {
			val /= BytesPerGiB
		}
		normalized[canonical] += val
	}
	return normalized
}
