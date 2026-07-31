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

	"github.com/google/go-cmp/cmp"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"google.golang.org/protobuf/testing/protocmp"
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

func TestConvertAggregatedQuotaToResourceCapacity(t *testing.T) {
	tests := []struct {
		name                  string
		nominalQuota          float64
		borrowingLimit        float64
		totalUsed             float64
		wantTotal             float64
		wantFree              float64
		wantFreeWithBorrowing float64
	}{
		{
			name:                  "Nominal with partial usage",
			nominalQuota:          100.0,
			borrowingLimit:        0.0,
			totalUsed:             40.0,
			wantTotal:             100.0,
			wantFree:              60.0,
			wantFreeWithBorrowing: 60.0,
		},
		{
			name:                  "Nominal with borrowing limit and partial usage",
			nominalQuota:          100.0,
			borrowingLimit:        50.0,
			totalUsed:             40.0,
			wantTotal:             100.0,
			wantFree:              60.0,
			wantFreeWithBorrowing: 110.0,
		},
		{
			name:                  "Fully used capacity",
			nominalQuota:          8.0,
			borrowingLimit:        0.0,
			totalUsed:             8.0,
			wantTotal:             8.0,
			wantFree:              0.0,
			wantFreeWithBorrowing: 0.0,
		},
		{
			name:                  "Over-utilized capacity clamped to zero free",
			nominalQuota:          10.0,
			borrowingLimit:        5.0,
			totalUsed:             15.0,
			wantTotal:             10.0,
			wantFree:              0.0,
			wantFreeWithBorrowing: 5.0,
		},
		{
			name:                  "Zero quota and usage",
			nominalQuota:          0.0,
			borrowingLimit:        0.0,
			totalUsed:             0.0,
			wantTotal:             0.0,
			wantFree:              0.0,
			wantFreeWithBorrowing: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aggregator := &ResourceQuotaAggregator{
				nominalQuota:   tt.nominalQuota,
				borrowingLimit: tt.borrowingLimit,
			}
			capMetric := convertAggregatedQuotaToResourceCapacity("nvidia.com/gpu", aggregator, tt.totalUsed)

			if capMetric.TotalCapacity != tt.wantTotal {
				t.Errorf("TotalCapacity = %v, want %v", capMetric.TotalCapacity, tt.wantTotal)
			}
			if capMetric.FreeCapacity != tt.wantFree {
				t.Errorf("FreeCapacity = %v, want %v", capMetric.FreeCapacity, tt.wantFree)
			}
			if capMetric.FreeCapacityWithBorrowing != tt.wantFreeWithBorrowing {
				t.Errorf("FreeCapacityWithBorrowing = %v, want %v", capMetric.FreeCapacityWithBorrowing, tt.wantFreeWithBorrowing)
			}
		})
	}
}

func TestMultiFlavorQueueOverview(t *testing.T) {
	rfSpot := createTestResourceFlavorTyped("flavor-l4-spot", map[string]string{
		"cloud.google.com/gke-accelerator":  "nvidia-l4",
		"cloud.google.com/gke-provisioning": "spot",
	})
	rfOnDemand := createTestResourceFlavorTyped("flavor-h100-ondemand", map[string]string{
		"cloud.google.com/gke-accelerator":  "nvidia-tesla-h100",
		"cloud.google.com/gke-provisioning": "standard",
	})
	rfMap := map[string]*kueuev1beta1.ResourceFlavor{
		"flavor-l4-spot":       rfSpot,
		"flavor-h100-ondemand": rfOnDemand,
	}

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
			FlavorsReservation: []kueuev1beta1.FlavorUsage{
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

	wantOverview := &spb.QueueOverview{
		QueueName: "dynamic-gpu-cq",
		// Summed CPU (128+64=192) and Memory (512+256=768) across all flavors, with distinct accelerator capacities for nvidia-l4 and nvidia-tesla-h100:
		QueueCapacity: []*spb.QueueResourceCapacity{
			{
				ResourceType:              "nvidia-l4",
				TotalCapacity:             32.0,
				FreeCapacity:              24.0,
				FreeCapacityWithBorrowing: 24.0,
			},
			{
				ResourceType:              "cpu",
				TotalCapacity:             192.0,
				FreeCapacity:              150.0,
				FreeCapacityWithBorrowing: 150.0,
			},
			{
				ResourceType:              "memory",
				TotalCapacity:             768.0,
				FreeCapacity:              600.0,
				FreeCapacityWithBorrowing: 600.0,
			},
			{
				ResourceType:              "nvidia-tesla-h100",
				TotalCapacity:             8.0,
				FreeCapacity:              6.0,
				FreeCapacityWithBorrowing: 6.0,
			},
		},
	}

	overview := buildQueueOverviewFromClusterQueue(cq, rfMap)
	if diff := cmp.Diff(wantOverview, overview, protocmp.Transform()); diff != "" {
		t.Errorf("buildQueueOverviewFromClusterQueue mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildQueueOverviewFromClusterQueue_NilAndEmpty(t *testing.T) {
	if got := buildQueueOverviewFromClusterQueue(nil, nil); got != nil {
		t.Errorf("buildQueueOverviewFromClusterQueue(nil) = %v, want nil", got)
	}

	emptyCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-cq"},
	}
	overview := buildQueueOverviewFromClusterQueue(emptyCQ, nil)
	if overview == nil {
		t.Fatalf("buildQueueOverviewFromClusterQueue(emptyCQ) is nil, want non-nil")
	}
	if overview.QueueName != "empty-cq" {
		t.Errorf("QueueName = %q, want 'empty-cq'", overview.QueueName)
	}
	if len(overview.QueueCapacity) != 0 {
		t.Errorf("QueueCapacity count = %d, want 0", len(overview.QueueCapacity))
	}
}

func defaultString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
