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

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func createTestQueueCapacity(resourceType string, total, free, freeBorrowing float64) *spb.QueueResourceCapacity {
	return &spb.QueueResourceCapacity{
		ResourceType:              resourceType,
		TotalCapacity:             total,
		FreeCapacity:              free,
		FreeCapacityWithBorrowing: freeBorrowing,
	}
}

func createTestQueueOverview(name string, capacities ...*spb.QueueResourceCapacity) *spb.QueueOverview {
	return &spb.QueueOverview{
		QueueName:     name,
		QueueCapacity: capacities,
	}
}

func createTestResourceFlavor(name string, nodeLabels map[string]string) *kueuev1beta1.ResourceFlavor {
	return &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: nodeLabels,
		},
	}
}

type testFlavorSpec struct {
	name   string
	quotas []kueuev1beta1.ResourceQuota
	used   []kueuev1beta1.ResourceUsage
}

func createTestClusterQueueMulti(name, cohort string, flavors ...testFlavorSpec) *kueuev1beta1.ClusterQueue {
	var flavorQuotas []kueuev1beta1.FlavorQuotas
	var flavorUsages []kueuev1beta1.FlavorUsage

	for _, f := range flavors {
		flavorQuotas = append(flavorQuotas, kueuev1beta1.FlavorQuotas{
			Name:      kueuev1beta1.ResourceFlavorReference(f.name),
			Resources: f.quotas,
		})
		flavorUsages = append(flavorUsages, kueuev1beta1.FlavorUsage{
			Name:      kueuev1beta1.ResourceFlavorReference(f.name),
			Resources: f.used,
		})
	}

	return &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kueuev1beta1.ClusterQueueSpec{
			Cohort: kueuev1beta1.CohortReference(cohort),
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{Flavors: flavorQuotas},
			},
		},
		Status: kueuev1beta1.ClusterQueueStatus{
			FlavorsReservation: flavorUsages,
		},
	}
}

func createTestClusterQueue(name, cohort, flavorName, cpuQuota, cpuUsed string, borrowingLimit *resource.Quantity) *kueuev1beta1.ClusterQueue {
	var rq kueuev1beta1.ResourceQuota
	if borrowingLimit != nil {
		rq = kueuev1beta1.ResourceQuota{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse(cpuQuota), BorrowingLimit: borrowingLimit}
	} else {
		rq = kueuev1beta1.ResourceQuota{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse(cpuQuota)}
	}
	return createTestClusterQueueMulti(name, cohort, testFlavorSpec{
		name:   flavorName,
		quotas: []kueuev1beta1.ResourceQuota{rq},
		used: []kueuev1beta1.ResourceUsage{
			{Name: corev1.ResourceCPU, Total: resource.MustParse(cpuUsed)},
		},
	})
}

func TestConvertAggregatedQuotaToResourceCapacity(t *testing.T) {
	type input struct {
		nominalQuota   float64
		borrowingLimit float64
		totalUsed      float64
	}
	type want struct {
		total             float64
		free              float64
		freeWithBorrowing float64
	}

	tests := []struct {
		name string
		in   input
		want want
	}{
		{
			name: "Nominal with partial usage",
			in: input{
				nominalQuota:   100.0,
				borrowingLimit: 0.0,
				totalUsed:      40.0,
			},
			want: want{
				total:             100.0,
				free:              60.0,
				freeWithBorrowing: 60.0,
			},
		},
		{
			name: "Nominal with borrowing limit and partial usage",
			in: input{
				nominalQuota:   100.0,
				borrowingLimit: 50.0,
				totalUsed:      40.0,
			},
			want: want{
				total:             100.0,
				free:              60.0,
				freeWithBorrowing: 110.0,
			},
		},
		{
			name: "Fully used capacity",
			in: input{
				nominalQuota:   8.0,
				borrowingLimit: 0.0,
				totalUsed:      8.0,
			},
			want: want{
				total:             8.0,
				free:              0.0,
				freeWithBorrowing: 0.0,
			},
		},
		{
			name: "Over-utilized capacity clamped to zero free",
			in: input{
				nominalQuota:   10.0,
				borrowingLimit: 5.0,
				totalUsed:      15.0,
			},
			want: want{
				total:             10.0,
				free:              0.0,
				freeWithBorrowing: 0.0,
			},
		},
		{
			name: "Zero quota and usage",
			in: input{
				nominalQuota:   0.0,
				borrowingLimit: 0.0,
				totalUsed:      0.0,
			},
			want: want{
				total:             0.0,
				free:              0.0,
				freeWithBorrowing: 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aggregator := &ResourceQuotaAggregator{
				nominalQuota:   tt.in.nominalQuota,
				borrowingLimit: tt.in.borrowingLimit,
			}
			capMetric := convertAggregatedQuotaToResourceCapacity("nvidia.com/gpu", aggregator, tt.in.totalUsed, nil)

			if capMetric.TotalCapacity != tt.want.total {
				t.Errorf("TotalCapacity = %v, want %v", capMetric.TotalCapacity, tt.want.total)
			}
			if capMetric.FreeCapacity != tt.want.free {
				t.Errorf("FreeCapacity = %v, want %v", capMetric.FreeCapacity, tt.want.free)
			}
			if capMetric.FreeCapacityWithBorrowing != tt.want.freeWithBorrowing {
				t.Errorf("FreeCapacityWithBorrowing = %v, want %v", capMetric.FreeCapacityWithBorrowing, tt.want.freeWithBorrowing)
			}
		})
	}

	t.Run("Nil aggregator safety", func(t *testing.T) {
		capMetric := convertAggregatedQuotaToResourceCapacity("cpu", nil, 5.0, nil)
		if capMetric.TotalCapacity != 0 || capMetric.FreeCapacity != 0 || capMetric.FreeCapacityWithBorrowing != 0 {
			t.Errorf("convertAggregatedQuotaToResourceCapacity(nil) = %v, want all zero capacities", capMetric)
		}
	})

	t.Run("Cohort capacity clamping", func(t *testing.T) {
		aggregator := &ResourceQuotaAggregator{
			nominalQuota:   100.0,
			borrowingLimit: 50.0,
		}
		cohortFree := 30.0
		// LocalFree = (100 + 50) - 20 = 130. CohortFree = 30.
		// FreeCapacityWithBorrowing should be min(130, 30) = 30.
		capMetric := convertAggregatedQuotaToResourceCapacity("cpu", aggregator, 20.0, &cohortFree)
		if capMetric.FreeCapacityWithBorrowing != 30.0 {
			t.Errorf("FreeCapacityWithBorrowing = %v, want 30.0", capMetric.FreeCapacityWithBorrowing)
		}
	})
}

func TestMultiFlavorQueueOverview(t *testing.T) {
	rfSpot := createTestResourceFlavor("flavor-l4-spot", map[string]string{
		"cloud.google.com/gke-accelerator":  "nvidia-l4",
		"cloud.google.com/gke-provisioning": "spot",
	})
	rfOnDemand := createTestResourceFlavor("flavor-h100-ondemand", map[string]string{
		"cloud.google.com/gke-accelerator":  "nvidia-tesla-h100",
		"cloud.google.com/gke-provisioning": "standard",
	})
	rfMap := map[string]*kueuev1beta1.ResourceFlavor{
		"flavor-l4-spot":       rfSpot,
		"flavor-h100-ondemand": rfOnDemand,
	}

	cq := createTestClusterQueueMulti("dynamic-gpu-cq", "",
		testFlavorSpec{
			name: "flavor-l4-spot",
			quotas: []kueuev1beta1.ResourceQuota{
				{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("32")},
				{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("128")},
				{Name: corev1.ResourceMemory, NominalQuota: resource.MustParse("512Gi")},
			},
			used: []kueuev1beta1.ResourceUsage{
				{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("8")},
				{Name: corev1.ResourceCPU, Total: resource.MustParse("28")},
				{Name: corev1.ResourceMemory, Total: resource.MustParse("112Gi")},
			},
		},
		testFlavorSpec{
			name: "flavor-h100-ondemand",
			quotas: []kueuev1beta1.ResourceQuota{
				{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("8")},
				{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("64")},
				{Name: corev1.ResourceMemory, NominalQuota: resource.MustParse("256Gi")},
			},
			used: []kueuev1beta1.ResourceUsage{
				{Name: corev1.ResourceName("nvidia.com/gpu"), Total: resource.MustParse("2")},
				{Name: corev1.ResourceCPU, Total: resource.MustParse("14")},
				{Name: corev1.ResourceMemory, Total: resource.MustParse("56Gi")},
			},
		},
	)

	// Summed CPU (128+64=192) and Memory (512+256=768) across all flavors, with distinct accelerator capacities for nvidia-l4 and nvidia-tesla-h100:
	wantOverview := createTestQueueOverview("dynamic-gpu-cq",
		createTestQueueCapacity("nvidia.com/gpu: nvidia-l4", 32.0, 24.0, 24.0),
		createTestQueueCapacity("cpu", 192.0, 150.0, 150.0),
		createTestQueueCapacity("memory", 768.0, 600.0, 600.0),
		createTestQueueCapacity("nvidia.com/gpu: nvidia-tesla-h100", 8.0, 6.0, 6.0),
	)

	overview := buildQueueOverviewFromClusterQueue(cq, rfMap, nil)
	if diff := cmp.Diff(wantOverview, overview, protocmp.Transform()); diff != "" {
		t.Errorf("buildQueueOverviewFromClusterQueue mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildQueueOverviewFromClusterQueue_NilAndEmpty(t *testing.T) {
	if got := buildQueueOverviewFromClusterQueue(nil, nil, nil); got != nil {
		t.Errorf("buildQueueOverviewFromClusterQueue(nil) = %v, want nil", got)
	}

	emptyCQ := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-cq"},
	}
	overview := buildQueueOverviewFromClusterQueue(emptyCQ, nil, nil)
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

func TestBuildResourceFlavorMap(t *testing.T) {
	t.Run("nil cache returns empty map", func(t *testing.T) {
		got := buildResourceFlavorMap(nil)
		if len(got) != 0 {
			t.Errorf("buildResourceFlavorMap(nil) len = %d, want 0", len(got))
		}
	})

	t.Run("uninitialized cache returns empty map", func(t *testing.T) {
		c := cache.NewKueueCache()
		got := buildResourceFlavorMap(c)
		if len(got) != 0 {
			t.Errorf("buildResourceFlavorMap(empty cache) len = %d, want 0", len(got))
		}
	})

	t.Run("populated cache builds map by name", func(t *testing.T) {
		c := cache.NewKueueCache()
		rf := createTestResourceFlavor("gpu-flavor", map[string]string{
			"cloud.google.com/gke-accelerator": "nvidia-h100",
		})
		c.ResourceFlavors.Set("gpu-flavor", rf)

		got := buildResourceFlavorMap(c)
		if len(got) != 1 {
			t.Fatalf("buildResourceFlavorMap len = %d, want 1", len(got))
		}
		if got["gpu-flavor"] != rf {
			t.Errorf("buildResourceFlavorMap['gpu-flavor'] = %v, want %v", got["gpu-flavor"], rf)
		}
	})
}

func TestBuildQueueOverviews(t *testing.T) {
	t.Run("nil cache returns nil", func(t *testing.T) {
		got := buildQueueOverviews(nil, nil)
		if got != nil {
			t.Errorf("buildQueueOverviews(nil) = %v, want nil", got)
		}
	})

	t.Run("uninitialized cache returns empty slice", func(t *testing.T) {
		c := cache.NewKueueCache()
		got := buildQueueOverviews(c, nil)
		if len(got) != 0 {
			t.Errorf("buildQueueOverviews(empty cache) len = %d, want 0", len(got))
		}
	})

	t.Run("populated cache builds queue overviews", func(t *testing.T) {
		c := cache.NewKueueCache()
		cq := &kueuev1beta1.ClusterQueue{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cq"},
		}
		c.ClusterQueues.Set("test-cq", cq)

		got := buildQueueOverviews(c, nil)
		if len(got) != 1 {
			t.Fatalf("buildQueueOverviews len = %d, want 1", len(got))
		}
		if got[0].QueueName != "test-cq" {
			t.Errorf("QueueName = %q, want 'test-cq'", got[0].QueueName)
		}
	})
}

func float64Ptr(f float64) *float64 {
	return &f
}

func TestCohortResourceBorrowing(t *testing.T) {
	borrowingLimit50 := resource.MustParse("50")

	tests := []struct {
		name          string
		clusterQueues []*kueuev1beta1.ClusterQueue
		rfMap         map[string]*kueuev1beta1.ResourceFlavor
		want          []*spb.QueueOverview
	}{
		{
			name: "Shared cohort borrowing calculation",
			clusterQueues: []*kueuev1beta1.ClusterQueue{
				createTestClusterQueue("queue-1", "shared-cohort", "default-flavor", "100", "40", &borrowingLimit50),
				createTestClusterQueue("queue-2", "shared-cohort", "default-flavor", "100", "130", &borrowingLimit50),
			},
			want: []*spb.QueueOverview{
				createTestQueueOverview("queue-1",
					createTestQueueCapacity("cpu", 100.0, 60.0, 30.0),
				),
				createTestQueueOverview("queue-2",
					createTestQueueCapacity("cpu", 100.0, 0.0, 20.0),
				),
			},
		},
		{
			name: "Standalone queue without cohort",
			clusterQueues: []*kueuev1beta1.ClusterQueue{
				createTestClusterQueue("queue-standalone", "", "default-flavor", "100", "40", &borrowingLimit50),
			},
			want: []*spb.QueueOverview{
				createTestQueueOverview("queue-standalone",
					createTestQueueCapacity("cpu", 100.0, 60.0, 110.0),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cache.NewKueueCache()
			for _, cq := range tt.clusterQueues {
				c.ClusterQueues.Set(cq.Name, cq)
			}
			got := buildQueueOverviews(c, tt.rfMap)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform(), cmpopts.SortSlices(func(a, b *spb.QueueOverview) bool {
				return a.QueueName < b.QueueName
			})); diff != "" {
				t.Errorf("buildQueueOverviews mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("CohortResourceCapacities FreeCapacityFor lookup and safety", func(t *testing.T) {
		capacitiesTests := []struct {
			name         string
			capacities   *CohortResourceCapacities
			resourceType string
			want         *float64
		}{
			{
				name:         "Nil capacities returns nil",
				capacities:   nil,
				resourceType: "cpu",
				want:         nil,
			},
			{
				name: "Missing resource type returns nil",
				capacities: &CohortResourceCapacities{
					quotas: map[string]*CohortResourceQuota{
						"cpu": {NominalQuota: 100.0, UsedCapacity: 20.0},
					},
				},
				resourceType: "memory",
				want:         nil,
			},
			{
				name: "Existing resource type returns free capacity pointer",
				capacities: &CohortResourceCapacities{
					quotas: map[string]*CohortResourceQuota{
						"cpu": {NominalQuota: 100.0, UsedCapacity: 20.0},
					},
				},
				resourceType: "cpu",
				want:         float64Ptr(80.0),
			},
		}

		for _, tt := range capacitiesTests {
			t.Run(tt.name, func(t *testing.T) {
				got := tt.capacities.FreeCapacityFor(tt.resourceType)
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("FreeCapacityFor(%q) mismatch (-want +got):\n%s", tt.resourceType, diff)
				}
			})
		}
	})

	t.Run("CohortResourceCapacities quotaOf nil safety", func(t *testing.T) {
		var capacities *CohortResourceCapacities
		if got := capacities.quotaOf("cpu"); got != nil {
			t.Errorf("quotaOf on nil receiver = %v, want nil", got)
		}
	})
}
