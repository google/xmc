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
	"k8s.io/apimachinery/pkg/api/resource"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/internal/common/resourceaccumulator"
)

var acceleratorLabelKeys = []string{
	"cloud.google.com/gke-accelerator",
	"cloud.google.com/gke-gpu-type",
	"cloud.google.com/gke-tpu-accelerator",
	"kueue.x-k8s.io/gpack-gpu",
	"gpu.nvidia.com/class",
}

// resolveResourceType resolves generic resource names like "nvidia.com/gpu" to compound accelerator names
// (e.g. "nvidia.com/gpu: nvidia-tesla-h100") using the ResourceFlavor's nodeLabels if present.
// Non-accelerator resources (like "cpu" or "memory") remain unchanged.
func resolveResourceType(resourceName, flavorName string, rfMap map[string]*kueuev1beta1.ResourceFlavor) string {
	if !resourceaccumulator.IsAcceleratorResource(resourceName) {
		return resourceName
	}
	if rf := rfMap[flavorName]; rf != nil {
		for _, labelKey := range acceleratorLabelKeys {
			if acc := rf.Spec.NodeLabels[labelKey]; acc != "" {
				return resourceaccumulator.FormatCompoundAccelerator(resourceName, acc)
			}
		}
	}
	if resourceaccumulator.IsSpecificAcceleratorName(flavorName) {
		return resourceaccumulator.FormatCompoundAccelerator(resourceName, flavorName)
	}
	return resourceName
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
	if resourceaccumulator.IsMemoryOrStorageResource(resourceType) {
		return val / resourceaccumulator.BytesPerGiB
	}
	return val
}
