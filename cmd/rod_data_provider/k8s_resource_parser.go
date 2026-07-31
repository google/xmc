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

// extractTotalResourceUsageByResourceType calculates total used capacity per resource type across all flavors in ClusterQueueStatus.
// For each flavor and resource type, it computes max(usage, reservation) and sums them across flavors.
func extractTotalResourceUsageByResourceType(status kueuev1beta1.ClusterQueueStatus) map[string]float64 {
	// 1. Record reservations per "flavorName/resourceType"
	usageByFlavorAndResource := make(map[string]float64)
	for _, flavorStatus := range status.FlavorsReservation {
		for _, resourceUsage := range flavorStatus.Resources {
			resourceType := string(resourceUsage.Name)
			key := string(flavorStatus.Name) + "/" + resourceType
			usageByFlavorAndResource[key] = parseK8sResourceVal(resourceType, resourceUsage.Total.String())
		}
	}

	// 2. Update with actual usage if greater than reservation
	for _, flavorStatus := range status.FlavorsUsage {
		for _, resourceUsage := range flavorStatus.Resources {
			resourceType := string(resourceUsage.Name)
			key := string(flavorStatus.Name) + "/" + resourceType
			usageQty := parseK8sResourceVal(resourceType, resourceUsage.Total.String())
			if usageQty > usageByFlavorAndResource[key] {
				usageByFlavorAndResource[key] = usageQty
			}
		}
	}

	// 3. Aggregate totals by resource type across all flavors
	totalUsageByResourceType := make(map[string]float64)
	for key, usedQty := range usageByFlavorAndResource {
		parts := strings.Split(key, "/")
		if len(parts) == 2 {
			resourceType := parts[1]
			totalUsageByResourceType[resourceType] += usedQty
		}
	}

	return totalUsageByResourceType
}

// parseK8sResourceVal parses Kubernetes quantity strings (e.g. "8Gi", "1024Ki", "1000m", "4") into a normalized float64 value.
// Memory and storage quantities are normalized to GiB, while CPU, GPU, and count quantities are normalized to Cores/Units.
func parseK8sResourceVal(resourceType, quantityStr string) float64 {
	s := strings.TrimSpace(quantityStr)
	if s == "" || s == "-" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	if isMemoryOrStorageResource(resourceType) {
		return float64(q.Value()) / (1024 * 1024 * 1024)
	}
	return q.AsApproximateFloat64()
}

func isMemoryOrStorageResource(resourceType string) bool {
	lower := strings.ToLower(resourceType)
	return strings.Contains(lower, "memory") || strings.Contains(lower, "storage")
}
