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
	"strings"

	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

const bytesPerGiB = 1024 * 1024 * 1024

// resolveResourceType resolves generic resource names like "nvidia.com/gpu" to specific accelerator names (e.g. "nvidia-tesla-h100")
// using the ResourceFlavor's nodeLabels if present. Non-accelerator resources (like "cpu" or "memory") remain unchanged.
func resolveResourceType(resourceName, flavorName string, rfMap map[string]*kueuev1beta1.ResourceFlavor) string {
	if !isAcceleratorResource(resourceName) {
		return resourceName
	}
	if rfMap != nil {
		if rf := rfMap[flavorName]; rf != nil {
			for _, labelKey := range acceleratorLabelKeys {
				if acc := rf.Spec.NodeLabels[labelKey]; acc != "" {
					return acc
				}
			}
		}
	}
	if isSpecificAcceleratorName(flavorName) {
		return flavorName
	}
	return resourceName
}

func isSpecificAcceleratorName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)

	if isGenericAcceleratorFlavor(lower) {
		return false
	}

	for _, kw := range longAcceleratorKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	for _, kw := range shortAcceleratorKeywords {
		if matchesShortAcceleratorKeyword(lower, kw) {
			return true
		}
	}

	if strings.Contains(lower, "gpu") || strings.Contains(lower, "tpu") || strings.Contains(lower, "nvidia") || strings.Contains(lower, "accelerator") {
		return true
	}

	return false
}

func isGenericAcceleratorFlavor(lower string) bool {
	for _, exact := range genericResourceNames {
		if lower == exact {
			return true
		}
	}
	for _, kw := range genericKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func matchesShortAcceleratorKeyword(s, kw string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], kw)
		if i == -1 {
			return false
		}
		pos := idx + i
		prevOK := (pos == 0) || !isASCIILetter(s[pos-1])
		nextPos := pos + len(kw)
		nextOK := (nextPos == len(s)) || !isASCIIAlphanumeric(s[nextPos])
		if prevOK && nextOK {
			return true
		}
		idx = pos + 1
	}
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isASCIIAlphanumeric(c byte) bool {
	return isASCIILetter(c) || (c >= '0' && c <= '9')
}

func isAcceleratorResource(resName string) bool {
	lower := strings.ToLower(resName)
	return strings.Contains(lower, "gpu") || strings.Contains(lower, "tpu") || strings.Contains(lower, "accelerator")
}

// extractTotalResourceUsageByResourceType calculates total used capacity per resource type across all flavors in ClusterQueueStatus.
// Kueue tracks admitted quota in FlavorsReservation (with fallback to FlavorsUsage for older Kueue versions).
// Specific accelerator types (e.g. "nvidia-tesla-h100") are resolved using the provided ResourceFlavor map.
func extractTotalResourceUsageByResourceType(status kueuev1beta1.ClusterQueueStatus, rfMap map[string]*kueuev1beta1.ResourceFlavor) map[string]float64 {
	totalUsageByResourceType := make(map[string]float64)

	flavorSource := status.FlavorsReservation
	if len(flavorSource) == 0 {
		flavorSource = status.FlavorsUsage
	}

	for _, flavorStatus := range flavorSource {
		flavorName := string(flavorStatus.Name)
		for _, resourceUsage := range flavorStatus.Resources {
			rawResourceType := string(resourceUsage.Name)
			resolvedType := resolveResourceType(rawResourceType, flavorName, rfMap)
			usedQty := parseK8sResourceQuantity(rawResourceType, resourceUsage.Total)
			totalUsageByResourceType[resolvedType] += usedQty
		}
	}

	return totalUsageByResourceType
}

// parseK8sResourceQuantity normalizes a resource.Quantity into a float64 value.
// Memory and storage quantities are normalized to GiB, while CPU, GPU, and count quantities are normalized to Cores/Units.
func parseK8sResourceQuantity(resourceType string, q resource.Quantity) float64 {
	val := q.AsApproximateFloat64()
	if isMemoryOrStorageResource(resourceType) {
		return val / bytesPerGiB
	}
	return val
}

func isMemoryOrStorageResource(resourceType string) bool {
	lower := strings.ToLower(resourceType)
	return strings.Contains(lower, "memory") || strings.Contains(lower, "storage")
}
