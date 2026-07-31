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

// extractStatusUsageTotals calculates the total used capacity per resource type across all flavors in ClusterQueueStatus.
func extractStatusUsageTotals(status kueuev1beta1.ClusterQueueStatus) map[string]float64 {
	usageTotals := make(map[string]float64)

	// Build temporary map of flavor-level reservations: "flavor/resource" -> reservedVal
	resMap := make(map[string]float64)
	for _, flv := range status.FlavorsReservation {
		for _, res := range flv.Resources {
			resName := string(res.Name)
			key := string(flv.Name) + "/" + resName
			resMap[key] = parseK8sResourceVal(resName, res.Total.String())
		}
	}

	// Sum max(usage, reservation) per resource type across all flavors
	for _, flv := range status.FlavorsUsage {
		for _, res := range flv.Resources {
			resName := string(res.Name)
			key := string(flv.Name) + "/" + resName
			useVal := parseK8sResourceVal(resName, res.Total.String())
			usedVal := useVal
			if resVal, exists := resMap[key]; exists && resVal > usedVal {
				usedVal = resVal
			}
			usageTotals[resName] += usedVal
		}
	}

	return usageTotals
}

// parseK8sResourceVal parses Kubernetes quantity strings (e.g. "8Gi", "1024Ki", "1000m", "4") into a normalized float64 value.
// Memory and storage quantities are normalized to GiB, while CPU, GPU, and count quantities are normalized to Cores/Units.
func parseK8sResourceVal(resName, s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	// Memory / Storage resources: normalize to GiB
	if strings.Contains(strings.ToLower(resName), "memory") || strings.Contains(strings.ToLower(resName), "storage") {
		return float64(q.Value()) / (1024 * 1024 * 1024)
	}
	// CPU / GPU / Count resources: normalize to Cores / Units
	return q.AsApproximateFloat64()
}
