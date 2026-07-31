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
// Kueue tracks admitted quota in FlavorsReservation (with fallback to FlavorsUsage for older Kueue versions).
func extractTotalResourceUsageByResourceType(status kueuev1beta1.ClusterQueueStatus) map[string]float64 {
	totalUsageByResourceType := make(map[string]float64)

	flavorSource := status.FlavorsReservation
	if len(flavorSource) == 0 {
		flavorSource = status.FlavorsUsage
	}

	for _, flavorStatus := range flavorSource {
		for _, resourceUsage := range flavorStatus.Resources {
			resourceType := string(resourceUsage.Name)
			usedQty := parseK8sResourceVal(resourceType, resourceUsage.Total.String())
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
