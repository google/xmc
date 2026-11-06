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
	"google.golang.org/protobuf/testing/protocmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/internal/storage"
)

func buildFlavorCache(flavors map[string]string, cqFlavors []string) *cache.KueueCache {
	c := cache.NewKueueCache()
	for name, accLabel := range flavors {
		c.ResourceFlavors.Set(name, &kueuev1beta1.ResourceFlavor{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: kueuev1beta1.ResourceFlavorSpec{
				NodeLabels: map[string]string{"cloud.google.com/gke-accelerator": accLabel},
			},
		})
	}
	fqList := make([]kueuev1beta1.FlavorQuotas, 0, len(cqFlavors))
	for _, fName := range cqFlavors {
		fqList = append(fqList, kueuev1beta1.FlavorQuotas{
			Name: kueuev1beta1.ResourceFlavorReference(fName),
			Resources: []kueuev1beta1.ResourceQuota{
				{Name: corev1.ResourceName("nvidia.com/gpu"), NominalQuota: resource.MustParse("8")},
				{Name: corev1.ResourceCPU, NominalQuota: resource.MustParse("64")},
			},
		})
	}
	c.ClusterQueues.Set("gpu-cq", &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-cq"},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{{Flavors: fqList}},
		},
	})
	return c
}

func TestMapAuthorResourceAllocations(t *testing.T) {
	rows := []storage.AuthorResourceAllocation{
		{Author: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 2},
		{Author: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", PendingUnits: 4},
		{Author: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 8},
		{Author: "alice@example.com", ResourceType: "cpu", RunningUnits: 16},
		{Author: "alice@example.com", ResourceType: "memory", PendingUnits: 32.5},
		{Author: "carol@example.com", ResourceType: "nvidia.com/gpu", RunningUnits: 4},
	}

	singleFlavorCache := buildFlavorCache(map[string]string{"h100-flavor": "nvidia-tesla-h100"}, []string{"h100-flavor"})
	uniqueMap := buildUniqueQueueAcceleratorMap(singleFlavorCache, "gpu-cq")

	got := mapAuthorResourceAllocations(rows, uniqueMap)
	want := []*spb.UserResourceAllocation{
		{User: "alice@example.com", ResourceType: "memory", RunningUnits: 0, PendingUnits: 32.5},
		{User: "alice@example.com", ResourceType: "cpu", RunningUnits: 16, PendingUnits: 0},
		{User: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 8, PendingUnits: 0},
		{User: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 2, PendingUnits: 4},
		{User: "carol@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 4, PendingUnits: 0},
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("mapAuthorResourceAllocations(single flavor) mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildUniqueQueueAcceleratorMap_MultiFlavorKeepsGeneric(t *testing.T) {
	multiFlavorCache := buildFlavorCache(
		map[string]string{"h100-flavor": "nvidia-tesla-h100", "l4-flavor": "nvidia-l4"},
		[]string{"h100-flavor", "l4-flavor"},
	)
	uniqueMap := buildUniqueQueueAcceleratorMap(multiFlavorCache, "gpu-cq")
	if len(uniqueMap) != 0 {
		t.Errorf("buildUniqueQueueAcceleratorMap(multi flavor) = %v, want empty map", uniqueMap)
	}
}

func TestCompareUserResourceAllocations(t *testing.T) {
	tests := []struct {
		name string
		a, b *spb.UserResourceAllocation
		want int
	}{
		{
			name: "higher total first",
			a:    &spb.UserResourceAllocation{User: "a", RunningUnits: 1, PendingUnits: 5},
			b:    &spb.UserResourceAllocation{User: "b", RunningUnits: 4},
			want: -1,
		},
		{
			name: "equal total prefers more running",
			a:    &spb.UserResourceAllocation{User: "a", RunningUnits: 1, PendingUnits: 3},
			b:    &spb.UserResourceAllocation{User: "b", RunningUnits: 4},
			want: 1,
		},
		{
			name: "then user name",
			a:    &spb.UserResourceAllocation{User: "a", RunningUnits: 4},
			b:    &spb.UserResourceAllocation{User: "b", RunningUnits: 4},
			want: -1,
		},
		{
			name: "then resource type",
			a:    &spb.UserResourceAllocation{User: "a", ResourceType: "memory", RunningUnits: 4},
			b:    &spb.UserResourceAllocation{User: "a", ResourceType: "cpu", RunningUnits: 4},
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareUserResourceAllocations(tc.a, tc.b); got != tc.want {
				t.Errorf("compareUserResourceAllocations() = %d, want %d", got, tc.want)
			}
		})
	}
}
