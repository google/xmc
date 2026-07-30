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
	"strconv"
	"strings"

	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// extractResourceTotalsFromStatus converts Kueue FlavorUsage into a map of resource names to raw total strings.
func extractResourceTotalsFromStatus(usage []kueuev1beta1.FlavorUsage) map[string]string {
	resTotals := make(map[string]string)
	for _, flv := range usage {
		for _, res := range flv.Resources {
			resName := string(res.Name)
			resTotals[resName] = res.Total.String()
		}
	}
	return resTotals
}

// parseResourceUsageVal parses a raw resource usage string into a float64 value.
func parseResourceUsageVal(s string) float64 {
	val, _ := parseK8sResourceVal(s)
	return val
}

// parseK8sResourceVal parses Kubernetes quantity strings (e.g. "8Gi", "1000m", "4") into a float64 value and unit.
func parseK8sResourceVal(s string) (float64, string) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, ""
	}
	unit := ""
	for _, u := range []string{"Gi", "Mi", "Ki", "m", "M", "k", "G"} {
		if strings.HasSuffix(s, u) {
			unit = u
			break
		}
	}
	numStr := strings.TrimSuffix(s, unit)
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, unit
	}
	if unit == "m" {
		return v / 1000.0, unit
	}
	if unit == "Mi" {
		return v / 1024.0, unit
	}
	return v, unit
}
