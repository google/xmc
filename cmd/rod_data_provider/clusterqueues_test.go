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
	"context"
	"testing"
	"time"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
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

func TestFetchClusterQueuesWithCache(t *testing.T) {
	ctx := context.Background()
	cache := NewKueueCache()

	cq1 := createTestClusterQueueTyped("cq-gpu-1", "gpu-cohort", "StrictFIFO", "8", "4", "2")
	cq2 := createTestClusterQueueTyped("cq-cpu-2", "cpu-cohort", "BestEffortFIFO", "0", "0", "0")
	lq1 := createTestLocalQueueTyped("lq-team-a", "team-a", "cq-gpu-1")
	rf1 := createTestResourceFlavorTyped("default-flavor", map[string]string{"cloud.google.com/gke-accelerator": "nvidia-tesla-h100"})

	cache.SetClusterQueue(cq1)
	cache.SetClusterQueue(cq2)
	cache.SetLocalQueue(lq1)
	cache.SetResourceFlavor(rf1)

	tests := []struct {
		name      string
		req       *spb.GetFittingQueuesRequest
		wantCount int
		wantQueue string
	}{
		{
			name:      "All Queues Without Filter",
			req:       &spb.GetFittingQueuesRequest{},
			wantCount: 2,
		},
		{
			name: "Filter By Queue Name Pattern",
			req: &spb.GetFittingQueuesRequest{
				QueueNamePattern: "gpu",
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Filter By Resource Requirement",
			req: &spb.GetFittingQueuesRequest{
				ResourceRequirements: []*spb.ResourceRequirement{
					{ResourceName: "nvidia.com/gpu", Quantity: "2"},
				},
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Filter Out When Resource Requirement Exceeds Free Capacity",
			req: &spb.GetFittingQueuesRequest{
				ResourceRequirements: []*spb.ResourceRequirement{
					{ResourceName: "nvidia.com/gpu", Quantity: "10"},
				},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FetchClusterQueues(ctx, cache, tt.req)
			if err != nil {
				t.Fatalf("FetchClusterQueues failed: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("FetchClusterQueues count = %d, want %d", len(got), tt.wantCount)
			}
			if tt.wantQueue != "" && len(got) > 0 && got[0].Name != tt.wantQueue {
				t.Errorf("FetchClusterQueues queue name = %s, want %s", got[0].Name, tt.wantQueue)
			}
		})
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
			got := computeFreeCapacityStr(tt.nominal, tt.reserved, tt.inUse)
			if got != tt.want {
				t.Errorf("computeFreeCapacityStr(%s, %s, %s) = %s, want %s", tt.nominal, tt.reserved, tt.inUse, got, tt.want)
			}
		})
	}
}
