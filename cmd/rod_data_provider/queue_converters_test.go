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
	"time"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func createTestClusterQueueTyped(name, cohort, strategy string, nominalGPU, inUseGPU, reservedGPU string) *kueuev1beta1.ClusterQueue {
	nomGPU := resource.MustParse(nominalGPU)
	resGPU := resource.MustParse(reservedGPU)
	useGPU := resource.MustParse(inUseGPU)

	return &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			Cohort:           kueuev1beta1.CohortReference(cohort),
			QueueingStrategy: kueuev1beta1.QueueingStrategy(strategy),
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					Flavors: []kueuev1beta1.FlavorQuotas{
						{
							Name: "default-flavor",
							Resources: []kueuev1beta1.ResourceQuota{
								{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: nomGPU},
							},
						},
					},
				},
			},
		},
		Status: kueuev1beta1.ClusterQueueStatus{
			AdmittedWorkloads: 1,
			PendingWorkloads:  1,
			Conditions: []metav1.Condition{
				{
					Type:               kueuev1beta1.ClusterQueueActive,
					Status:             metav1.ConditionTrue,
					Message:            "ClusterQueue is active",
					LastTransitionTime: metav1.NewTime(time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)),
				},
			},
			FlavorsReservation: []kueuev1beta1.FlavorUsage{
				{
					Name: "default-flavor",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resGPU},
					},
				},
			},
			FlavorsUsage: []kueuev1beta1.FlavorUsage{
				{
					Name: "default-flavor",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceName("nvidia.com/gpu"), Total: useGPU},
					},
				},
			},
		},
	}
}

func createTestLocalQueueTyped(name, namespace, clusterQueue string) *kueuev1beta1.LocalQueue {
	return &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: kueuev1beta1.LocalQueueSpec{
			ClusterQueue: kueuev1beta1.ClusterQueueReference(clusterQueue),
		},
	}
}

func createTestResourceFlavorTyped(name string, nodeLabels map[string]string) *kueuev1beta1.ResourceFlavor {
	return &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: nodeLabels,
		},
	}
}

func TestCapacityMath(t *testing.T) {
	tests := []struct {
		name     string
		nominal  string
		reserved string
		inUse    string
		want     string
	}{
		{
			name:     "Nominal Minus InUse",
			nominal:  "100",
			reserved: "20",
			inUse:    "40",
			want:     "60",
		},
		{
			name:     "Nominal Minus Reserved When Reserved > InUse",
			nominal:  "100",
			reserved: "60",
			inUse:    "40",
			want:     "40",
		},
		{
			name:     "GPU Capacity Math",
			nominal:  "8",
			reserved: "2",
			inUse:    "4",
			want:     "4",
		},
		{
			name:     "Memory Capacity Math",
			nominal:  "500Gi",
			reserved: "100Gi",
			inUse:    "200Gi",
			want:     "300Gi",
		},
		{
			name:     "Memory MiB Capacity Math",
			nominal:  "1024Mi",
			reserved: "256Mi",
			inUse:    "512Mi",
			want:     "512Mi",
		},
		{
			name:     "CPU Millicores Math",
			nominal:  "2000m",
			reserved: "500m",
			inUse:    "1000m",
			want:     "1000m",
		},
		{
			name:     "Zero Nominal Quota",
			nominal:  "0",
			reserved: "0",
			inUse:    "0",
			want:     "0",
		},
		{
			name:     "Empty Nominal Quota",
			nominal:  "",
			reserved: "0",
			inUse:    "0",
			want:     "0",
		},
		{
			name:     "Full Utilization",
			nominal:  "8",
			reserved: "8",
			inUse:    "8",
			want:     "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nomRes := resource.MustParse(defaultString(tt.nominal, "0"))
			resQuota := kueuev1beta1.ResourceQuota{
				Name:         corev1.ResourceName("nvidia.com/gpu"),
				NominalQuota: nomRes,
			}

			aggregator := &ResourceQuotaAggregator{}
			addFlavorResourceQuotaToAggregator(resQuota, aggregator)

			useVal := parseK8sResourceVal("nvidia.com/gpu", tt.inUse)
			resVal := parseK8sResourceVal("nvidia.com/gpu", tt.reserved)
			usedVal := resVal
			if useVal > usedVal {
				usedVal = useVal
			}

			capMetric := convertAggregatedQuotaToResourceCapacity("nvidia.com/gpu", aggregator, usedVal)
			wantVal := parseK8sResourceVal("nvidia.com/gpu", tt.want)
			if capMetric.FreeCapacity != wantVal {
				t.Errorf("convertAggregatedQuotaToResourceCapacity(%s, %s, %s).FreeCapacity = %v, want %v", tt.nominal, tt.reserved, tt.inUse, capMetric.FreeCapacity, wantVal)
			}
		})
	}
}

func TestMultiFlavorQueueOverview(t *testing.T) {
	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dynamic-gpu-cq",
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					Flavors: []kueuev1beta1.FlavorQuotas{
						{
							Name: "flavor-l4-spot",
							Resources: []kueuev1beta1.ResourceQuota{
								{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("32")},
								{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("128")},
								{Name: corev1.ResourceMemory, NominalQuota: resource.MustParse("512Gi")},
							},
						},
						{
							Name: "flavor-h100-ondemand",
							Resources: []kueuev1beta1.ResourceQuota{
								{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("8")},
								{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("64")},
								{Name: corev1.ResourceMemory, NominalQuota: resource.MustParse("256Gi")},
							},
						},
					},
				},
			},
		},
		Status: kueuev1beta1.ClusterQueueStatus{
			FlavorsUsage: []kueuev1beta1.FlavorUsage{
				{
					Name: "flavor-l4-spot",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("8")},
						{Name: corev1.ResourceCPU, Total: resource.MustParse("28")},
						{Name: corev1.ResourceMemory, Total: resource.MustParse("112Gi")},
					},
				},
				{
					Name: "flavor-h100-ondemand",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("2")},
						{Name: corev1.ResourceCPU, Total: resource.MustParse("14")},
						{Name: corev1.ResourceMemory, Total: resource.MustParse("56Gi")},
					},
				},
			},
		},
	}

	overview := buildQueueOverviewFromClusterQueue(cq)
	// Expect exactly 3 distinct resource types summed across all flavors: nvidia.com/gpu, cpu, memory
	if len(overview.QueueCapacity) != 3 {
		t.Fatalf("buildQueueOverviewFromClusterQueue capacity count = %d, want 3", len(overview.QueueCapacity))
	}

	capMap := make(map[string]*spb.QueueResourceCapacity)
	for _, c := range overview.QueueCapacity {
		capMap[c.ResourceType] = c
	}

	// 1. Verify GPU Quota Summation: 32 (L4 Spot) + 8 (H100 OnDemand) = 40 Total GPUs
	gpuCap, exists := capMap["nvidia.com/gpu"]
	if !exists {
		t.Fatalf("Missing nvidia.com/gpu capacity")
	}
	if gpuCap.TotalCapacity != 40.0 {
		t.Errorf("GPU TotalCapacity = %v, want 40.0", gpuCap.TotalCapacity)
	}
	// Used: 8 + 2 = 10, Free: 40 - 10 = 30
	if gpuCap.FreeCapacity != 30.0 {
		t.Errorf("GPU FreeCapacity = %v, want 30.0", gpuCap.FreeCapacity)
	}

	// 2. Verify CPU Quota Summation: 128 + 64 = 192 Total Cores
	cpuCap, exists := capMap["cpu"]
	if !exists {
		t.Fatalf("Missing cpu capacity")
	}
	if cpuCap.TotalCapacity != 192.0 {
		t.Errorf("CPU TotalCapacity = %v, want 192.0", cpuCap.TotalCapacity)
	}
	// Used: 28 + 14 = 42, Free: 192 - 42 = 150
	if cpuCap.FreeCapacity != 150.0 {
		t.Errorf("CPU FreeCapacity = %v, want 150.0", cpuCap.FreeCapacity)
	}

	// 3. Verify Memory Quota Summation: 512Gi + 256Gi = 768 Total GiB
	memCap, exists := capMap["memory"]
	if !exists {
		t.Fatalf("Missing memory capacity")
	}
	if memCap.TotalCapacity != 768.0 {
		t.Errorf("Memory TotalCapacity = %v, want 768.0", memCap.TotalCapacity)
	}
	// Used: 112Gi + 56Gi = 168Gi, Free: 768 - 168 = 600GiB
	if memCap.FreeCapacity != 600.0 {
		t.Errorf("Memory FreeCapacity = %v, want 600.0", memCap.FreeCapacity)
	}
}

func defaultString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
