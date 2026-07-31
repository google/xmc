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
	"testing"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func TestParseK8sResourceQuantity_MemoryNormalization(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantVal float64
	}{
		{name: "Gigabytes", input: "8Gi", wantVal: 8.0},
		{name: "Megabytes to GiB", input: "1024Mi", wantVal: 1.0},
		{name: "Kilobytes to GiB", input: "1048576Ki", wantVal: 1.0},
		{name: "Zero memory", input: "0", wantVal: 0.0},
		{name: "Empty string", input: "", wantVal: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q resource.Quantity
			if tt.input != "" {
				q = resource.MustParse(tt.input)
			}
			gotVal := parseK8sResourceQuantity("memory", q)
			if gotVal != tt.wantVal {
				t.Errorf("parseK8sResourceQuantity('memory', %q) = %v, want %v", tt.input, gotVal, tt.wantVal)
			}
		})
	}
}

func TestParseK8sResourceQuantity_CPUNormalization(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantVal float64
	}{
		{name: "Millicores 500m", input: "500m", wantVal: 0.5},
		{name: "Millicores 2000m", input: "2000m", wantVal: 2.0},
		{name: "Whole cores 4", input: "4", wantVal: 4.0},
		{name: "Zero cores", input: "0", wantVal: 0.0},
		{name: "Dash placeholder", input: "-", wantVal: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q resource.Quantity
			if tt.input != "" && tt.input != "-" {
				q = resource.MustParse(tt.input)
			}
			gotVal := parseK8sResourceQuantity("cpu", q)
			if gotVal != tt.wantVal {
				t.Errorf("parseK8sResourceQuantity('cpu', %q) = %v, want %v", tt.input, gotVal, tt.wantVal)
			}
		})
	}
}

func TestExtractTotalResourceUsageByResourceType_Reservation(t *testing.T) {
	status := kueuev1beta1.ClusterQueueStatus{
		FlavorsReservation: []kueuev1beta1.FlavorUsage{
			{
				Name: "flavor-spot",
				Resources: []kueuev1beta1.ResourceUsage{
					{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("8")},
					{Name: corev1.ResourceCPU, Total: resource.MustParse("32")},
				},
			},
			{
				Name: "flavor-ondemand",
				Resources: []kueuev1beta1.ResourceUsage{
					{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("4")},
					{Name: corev1.ResourceCPU, Total: resource.MustParse("16")},
				},
			},
		},
	}

	totals := extractTotalResourceUsageByResourceType(status, nil)

	// GPU Spot (8) + OnDemand (4) = 12 GPUs
	if got := totals["nvidia.com/gpu"]; got != 12.0 {
		t.Errorf("extractTotalResourceUsageByResourceType GPU = %v, want 12.0", got)
	}

	// CPU Spot (32) + OnDemand (16) = 48 Cores
	if got := totals["cpu"]; got != 48.0 {
		t.Errorf("extractTotalResourceUsageByResourceType CPU = %v, want 48.0", got)
	}
}

func TestExtractTotalResourceUsageByResourceType_FallbackToUsage(t *testing.T) {
	status := kueuev1beta1.ClusterQueueStatus{
		FlavorsReservation: []kueuev1beta1.FlavorUsage{}, // Empty reservation (older Kueue format)
		FlavorsUsage: []kueuev1beta1.FlavorUsage{
			{
				Name: "spot",
				Resources: []kueuev1beta1.ResourceUsage{
					{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("16")},
				},
			},
		},
	}

	totals := extractTotalResourceUsageByResourceType(status, nil)
	if totals["nvidia.com/gpu"] != 16.0 {
		t.Errorf("extractTotalResourceUsageByResourceType(usage-fallback) = %v, want 16.0", totals["nvidia.com/gpu"])
	}
}

func TestExtractTotalResourceUsageByResourceType_WithAcceleratorResolution(t *testing.T) {
	status := kueuev1beta1.ClusterQueueStatus{
		FlavorsReservation: []kueuev1beta1.FlavorUsage{
			{
				Name: "flavor-h100",
				Resources: []kueuev1beta1.ResourceUsage{
					{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("2")},
				},
			},
		},
	}
	rfMap := map[string]*kueuev1beta1.ResourceFlavor{
		"flavor-h100": {
			Spec: kueuev1beta1.ResourceFlavorSpec{
				NodeLabels: map[string]string{
					"cloud.google.com/gke-accelerator": "nvidia-tesla-h100",
				},
			},
		},
	}

	totals := extractTotalResourceUsageByResourceType(status, rfMap)
	if got := totals["nvidia-tesla-h100"]; got != 2.0 {
		t.Errorf("Expected resolved accelerator type 'nvidia-tesla-h100' to have usage 2.0, got %v", got)
	}
}
