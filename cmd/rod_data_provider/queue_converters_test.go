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
	nomCPU := resource.MustParse("100")
	resGPU := resource.MustParse(reservedGPU)
	resCPU := resource.MustParse("20")
	useGPU := resource.MustParse(inUseGPU)
	useCPU := resource.MustParse("40")

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
								{Name: corev1.ResourceCPU, NominalQuota: nomCPU},
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
						{Name: corev1.ResourceCPU, Total: resCPU},
					},
				},
			},
			FlavorsUsage: []kueuev1beta1.FlavorUsage{
				{
					Name: "default-flavor",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceName("nvidia.com/gpu"), Total: useGPU},
						{Name: corev1.ResourceCPU, Total: useCPU},
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
			usageMap := map[string]string{formatFlavorResourceKey("default-flavor", "nvidia.com/gpu"): tt.inUse}
			resMap := map[string]string{formatFlavorResourceKey("default-flavor", "nvidia.com/gpu"): tt.reserved}

			capMetric := buildResourceCapacity("default-flavor", resQuota, usageMap, resMap)
			wantVal := parseK8sResourceVal("nvidia.com/gpu", tt.want)
			if capMetric.FreeCapacity != wantVal {
				t.Errorf("buildResourceCapacity(%s, %s, %s).FreeCapacity = %v, want %v", tt.nominal, tt.reserved, tt.inUse, capMetric.FreeCapacity, wantVal)
			}
		})
	}
}

func TestMultiFlavorQueueOverview(t *testing.T) {
	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "multi-flavor-cq",
		},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					Flavors: []kueuev1beta1.FlavorQuotas{
						{
							Name: "on-demand",
							Resources: []kueuev1beta1.ResourceQuota{
								{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("100")},
							},
						},
						{
							Name: "spot",
							Resources: []kueuev1beta1.ResourceQuota{
								{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("200")},
							},
						},
					},
				},
			},
		},
		Status: kueuev1beta1.ClusterQueueStatus{
			FlavorsUsage: []kueuev1beta1.FlavorUsage{
				{
					Name: "on-demand",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceCPU, Total: resource.MustParse("30")},
					},
				},
				{
					Name: "spot",
					Resources: []kueuev1beta1.ResourceUsage{
						{Name: corev1.ResourceCPU, Total: resource.MustParse("80")},
					},
				},
			},
		},
	}

	overview := buildQueueOverviewFromClusterQueue(cq)
	if len(overview.QueueCapacity) != 2 {
		t.Fatalf("buildQueueOverviewFromClusterQueue capacity count = %d, want 2", len(overview.QueueCapacity))
	}

	// Verify on-demand flavor capacity (Nominal: 100, Used: 30, Free: 70)
	onDemandCap := overview.QueueCapacity[0]
	if onDemandCap.TotalCapacity != 100.0 || onDemandCap.FreeCapacity != 70.0 {
		t.Errorf("on-demand capacity = Total: %v, Free: %v; want Total: 100, Free: 70", onDemandCap.TotalCapacity, onDemandCap.FreeCapacity)
	}

	// Verify spot flavor capacity (Nominal: 200, Used: 80, Free: 120)
	spotCap := overview.QueueCapacity[1]
	if spotCap.TotalCapacity != 200.0 || spotCap.FreeCapacity != 120.0 {
		t.Errorf("spot capacity = Total: %v, Free: %v; want Total: 200, Free: 120", spotCap.TotalCapacity, spotCap.FreeCapacity)
	}
}

func defaultString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
