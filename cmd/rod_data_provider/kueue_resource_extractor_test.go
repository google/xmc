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
		input   resource.Quantity
		wantVal float64
	}{
		{name: "Gigabytes", input: resource.MustParse("8Gi"), wantVal: 8.0},
		{name: "Megabytes to GiB", input: resource.MustParse("1024Mi"), wantVal: 1.0},
		{name: "Kilobytes to GiB", input: resource.MustParse("1048576Ki"), wantVal: 1.0},
		{name: "Zero memory", input: resource.MustParse("0"), wantVal: 0.0},
		{name: "Zero-value Quantity", input: resource.Quantity{}, wantVal: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVal := parseK8sResourceQuantity("memory", tt.input)
			if gotVal != tt.wantVal {
				t.Errorf("parseK8sResourceQuantity('memory', %v) = %v, want %v", tt.input, gotVal, tt.wantVal)
			}
		})
	}
}

func TestParseK8sResourceQuantity_CPUNormalization(t *testing.T) {
	tests := []struct {
		name    string
		input   resource.Quantity
		wantVal float64
	}{
		{name: "Millicores 500m", input: resource.MustParse("500m"), wantVal: 0.5},
		{name: "Millicores 2000m", input: resource.MustParse("2000m"), wantVal: 2.0},
		{name: "Whole cores 4", input: resource.MustParse("4"), wantVal: 4.0},
		{name: "Zero cores", input: resource.MustParse("0"), wantVal: 0.0},
		{name: "Zero-value Quantity", input: resource.Quantity{}, wantVal: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVal := parseK8sResourceQuantity("cpu", tt.input)
			if gotVal != tt.wantVal {
				t.Errorf("parseK8sResourceQuantity('cpu', %v) = %v, want %v", tt.input, gotVal, tt.wantVal)
			}
		})
	}
}

func testFlavorMap(name, labelKey, labelVal string) map[string]*kueuev1beta1.ResourceFlavor {
	return map[string]*kueuev1beta1.ResourceFlavor{
		name: {
			Spec: kueuev1beta1.ResourceFlavorSpec{
				NodeLabels: map[string]string{labelKey: labelVal},
			},
		},
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
	rfMap := testFlavorMap("flavor-h100", "cloud.google.com/gke-accelerator", "nvidia-tesla-h100")

	totals := extractTotalResourceUsageByResourceType(status, rfMap)
	if got := totals["nvidia.com/gpu: nvidia-tesla-h100"]; got != 2.0 {
		t.Errorf("Expected resolved accelerator type 'nvidia.com/gpu: nvidia-tesla-h100' to have usage 2.0, got %v", got)
	}
}

func TestResolveResourceType(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		flavorName   string
		rfMap        map[string]*kueuev1beta1.ResourceFlavor
		want         string
	}{
		{
			name:         "Non-accelerator resource CPU remains unchanged",
			resourceName: "cpu",
			flavorName:   "nvidia-tesla-h100",
			rfMap:        testFlavorMap("nvidia-tesla-h100", "cloud.google.com/gke-accelerator", "nvidia-tesla-h100"),
			want:         "cpu",
		},
		{
			name:         "Multi-label cloud.google.com/gke-accelerator",
			resourceName: "nvidia.com/gpu",
			flavorName:   "flavor-1",
			rfMap:        testFlavorMap("flavor-1", "cloud.google.com/gke-accelerator", "nvidia-tesla-h100"),
			want:         "nvidia.com/gpu: nvidia-tesla-h100",
		},
		{
			name:         "Multi-label cloud.google.com/gke-accelerator nvidia-h100-mega-80gb",
			resourceName: "nvidia.com/gpu",
			flavorName:   "flavor-a3-mega",
			rfMap:        testFlavorMap("flavor-a3-mega", "cloud.google.com/gke-accelerator", "nvidia-h100-mega-80gb"),
			want:         "nvidia.com/gpu: nvidia-h100-mega-80gb",
		},
		{
			name:         "Multi-label cloud.google.com/gke-gpu-type",
			resourceName: "nvidia.com/gpu",
			flavorName:   "flavor-2",
			rfMap:        testFlavorMap("flavor-2", "cloud.google.com/gke-gpu-type", "nvidia-l4"),
			want:         "nvidia.com/gpu: nvidia-l4",
		},
		{
			name:         "Multi-label cloud.google.com/gke-tpu-accelerator",
			resourceName: "google.com/tpu",
			flavorName:   "flavor-3",
			rfMap:        testFlavorMap("flavor-3", "cloud.google.com/gke-tpu-accelerator", "tpu-v5p"),
			want:         "google.com/tpu: tpu-v5p",
		},
		{
			name:         "Multi-label kueue.x-k8s.io/gpack-gpu",
			resourceName: "nvidia.com/gpu",
			flavorName:   "flavor-4",
			rfMap:        testFlavorMap("flavor-4", "kueue.x-k8s.io/gpack-gpu", "gpack-h100"),
			want:         "nvidia.com/gpu: gpack-h100",
		},
		{
			name:         "Multi-label gpu.nvidia.com/class",
			resourceName: "gpu",
			flavorName:   "flavor-5",
			rfMap:        testFlavorMap("flavor-5", "gpu.nvidia.com/class", "h100"),
			want:         "gpu: h100",
		},
		{
			name:         "Fallback to flavorName when specific accelerator flavor name without node labels",
			resourceName: "nvidia.com/gpu",
			flavorName:   "nvidia-tesla-h100",
			rfMap:        nil,
			want:         "nvidia.com/gpu: nvidia-tesla-h100",
		},
		{
			name:         "Fallback to flavorName when TPU flavor name without node labels",
			resourceName: "google.com/tpu",
			flavorName:   "v5p-8",
			rfMap:        nil,
			want:         "google.com/tpu: v5p-8",
		},
		{
			name:         "Generic flavor name flavor-spot does not resolve to flavor name",
			resourceName: "nvidia.com/gpu",
			flavorName:   "flavor-spot",
			rfMap:        nil,
			want:         "nvidia.com/gpu",
		},
		{
			name:         "Generic flavor name spot does not resolve to flavor name",
			resourceName: "nvidia.com/gpu",
			flavorName:   "spot",
			rfMap:        nil,
			want:         "nvidia.com/gpu",
		},
		{
			name:         "TPU topology label cloud.google.com/gke-tpu-topology is ignored during accelerator resolution",
			resourceName: "google.com/tpu",
			flavorName:   "flavor-tpu",
			rfMap:        testFlavorMap("flavor-tpu", "cloud.google.com/gke-tpu-topology", "2x2x1"),
			want:         "google.com/tpu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveResourceType(tt.resourceName, tt.flavorName, tt.rfMap)
			if got != tt.want {
				t.Errorf("resolveResourceType(%q, %q, ...) = %q, want %q", tt.resourceName, tt.flavorName, got, tt.want)
			}
		})
	}
}
