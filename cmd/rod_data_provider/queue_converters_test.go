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
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"google.golang.org/protobuf/testing/protocmp"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

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
			capMetric := convertAggregatedQuotaToResourceCapacity("nvidia.com/gpu", aggregator, tt.in.totalUsed)

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
		capMetric := convertAggregatedQuotaToResourceCapacity("cpu", nil, 5.0)
		if capMetric.TotalCapacity != 0 || capMetric.FreeCapacity != 0 || capMetric.FreeCapacityWithBorrowing != 0 {
			t.Errorf("convertAggregatedQuotaToResourceCapacity(nil) = %v, want all zero capacities", capMetric)
		}
	})
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
				ResourceType:              "nvidia.com/gpu: nvidia-l4",
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
				ResourceType:              "nvidia.com/gpu: nvidia-tesla-h100",
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
		rf := createTestResourceFlavorTyped("gpu-flavor", map[string]string{
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

func TestBuildQueueOverviewFromClusterQueue_AcceleratorResolution(t *testing.T) {
	tests := []struct {
		name     string
		cq       *kueuev1beta1.ClusterQueue
		rfMap    map[string]*kueuev1beta1.ResourceFlavor
		wantType string
	}{
		{
			name: "gke-gpu-type label resolution",
			cq: &kueuev1beta1.ClusterQueue{
				ObjectMeta: metav1.ObjectMeta{Name: "cq-l4"},
				Spec: kueuev1beta1.ClusterQueueSpec{
					ResourceGroups: []kueuev1beta1.ResourceGroup{
						{
							Flavors: []kueuev1beta1.FlavorQuotas{
								{
									Name: "flavor-l4",
									Resources: []kueuev1beta1.ResourceQuota{
										{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("8")},
									},
								},
							},
						},
					},
				},
			},
			rfMap: map[string]*kueuev1beta1.ResourceFlavor{
				"flavor-l4": createTestResourceFlavorTyped("flavor-l4", map[string]string{
					"cloud.google.com/gke-gpu-type": "nvidia-l4",
				}),
			},
			wantType: "nvidia.com/gpu: nvidia-l4",
		},
		{
			name: "flavorName fallback resolution",
			cq: &kueuev1beta1.ClusterQueue{
				ObjectMeta: metav1.ObjectMeta{Name: "cq-h100"},
				Spec: kueuev1beta1.ClusterQueueSpec{
					ResourceGroups: []kueuev1beta1.ResourceGroup{
						{
							Flavors: []kueuev1beta1.FlavorQuotas{
								{
									Name: "nvidia-tesla-h100",
									Resources: []kueuev1beta1.ResourceQuota{
										{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("16")},
									},
								},
							},
						},
					},
				},
			},
			rfMap:    nil,
			wantType: "nvidia.com/gpu: nvidia-tesla-h100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overview := buildQueueOverviewFromClusterQueue(tt.cq, tt.rfMap)
			if overview == nil || len(overview.QueueCapacity) != 1 {
				t.Fatalf("buildQueueOverviewFromClusterQueue() unexpected overview: %v", overview)
			}
			if got := overview.QueueCapacity[0].ResourceType; got != tt.wantType {
				t.Errorf("ResourceType = %q, want %q", got, tt.wantType)
			}
		})
	}
}
