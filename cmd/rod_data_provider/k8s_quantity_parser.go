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

// formatFlavorResourceKey constructs a unique lookup key for a resource within a flavor (e.g. "default-flavor/nvidia.com/gpu").
func formatFlavorResourceKey(flavorName, resourceName string) string {
	return flavorName + "/" + resourceName
}

// extractResourceTotalsFromStatus converts Kueue FlavorUsage into a map of "flavorName/resourceName" keys to raw total strings.
func extractResourceTotalsFromStatus(usage []kueuev1beta1.FlavorUsage) map[string]string {
	resTotals := make(map[string]string)
	for _, flv := range usage {
		for _, res := range flv.Resources {
			key := formatFlavorResourceKey(string(flv.Name), string(res.Name))
			resTotals[key] = res.Total.String()
		}
	}
	return resTotals
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
