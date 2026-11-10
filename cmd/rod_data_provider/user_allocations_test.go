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
	"k8s.io/apimachinery/pkg/api/resource"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"
)

// gpuQueueCache returns a cache with cluster queue "gpu-cq" offering nvidia.com/gpu in each flavor.
func gpuQueueCache(flavors ...*kueuev1beta1.ResourceFlavor) *cache.KueueCache {
	c := cache.NewKueueCache()
	specs := make([]testFlavorSpec, 0, len(flavors))
	for _, rf := range flavors {
		c.ResourceFlavors.Set(rf.Name, rf)
		specs = append(specs, testFlavorSpec{
			name:   rf.Name,
			quotas: []kueuev1beta1.ResourceQuota{{Name: "nvidia.com/gpu", NominalQuota: resource.MustParse("8")}},
		})
	}
	c.ClusterQueues.Set("gpu-cq", createTestClusterQueueMulti("gpu-cq", "", specs...))
	return c
}

func activeWorkUnit(creator string, wuState essmsgpb.WorkUnitStatus_WorkUnitState, spec storagetest.ActiveResourceSpec) storage.ActiveWorkUnitResources {
	return storage.ActiveWorkUnitResources{Creator: creator, ExecutionState: wuState, Executable: spec.Executable()}
}

func TestAggregateUserAllocations(t *testing.T) {
	const alice, bob, carol, dave = "alice@example.com", "bob@example.com", "carol@example.com", "dave@example.com"
	const h100 = "nvidia.com/gpu: nvidia-tesla-h100"
	workUnits := []storage.ActiveWorkUnitResources{
		activeWorkUnit(bob, essmsgpb.WorkUnitStatus_RUNNING, storagetest.ActiveResourceSpec{GPUType: "nvidia-tesla-h100", GPUCount: 2}),
		activeWorkUnit(bob, essmsgpb.WorkUnitStatus_SCHEDULING, storagetest.ActiveResourceSpec{GPUType: "nvidia-tesla-h100", GPUCount: 4}),
		activeWorkUnit(alice, essmsgpb.WorkUnitStatus_STARTING, storagetest.ActiveResourceSpec{GPUType: "nvidia-tesla-h100", GPUCount: 8}),
		activeWorkUnit(alice, essmsgpb.WorkUnitStatus_RUNNING, storagetest.ActiveResourceSpec{CPUCores: 16}),
		activeWorkUnit(alice, essmsgpb.WorkUnitStatus_INITIALIZING, storagetest.ActiveResourceSpec{MemoryGiB: 32.5}),
		activeWorkUnit(carol, essmsgpb.WorkUnitStatus_RUNNING, storagetest.ActiveResourceSpec{GPUCount: 4}),
		activeWorkUnit(dave, essmsgpb.WorkUnitStatus_SCHEDULING, storagetest.ActiveResourceSpec{TPUType: "tpu-v5-lite-podslice", TPUTopology: "2x2"}),
	}
	h100Flavor := createTestResourceFlavor("h100-flavor", map[string]string{"cloud.google.com/gke-accelerator": "nvidia-tesla-h100"})
	l4Flavor := createTestResourceFlavor("l4-flavor", map[string]string{"cloud.google.com/gke-accelerator": "nvidia-l4"})
	tests := []struct {
		name         string
		cache        *cache.KueueCache
		clusterQueue string
		wantCarol    string
	}{
		{name: "single gpu flavor attributes generic requests to its model", cache: gpuQueueCache(h100Flavor), clusterQueue: "gpu-cq", wantCarol: h100},
		{name: "several labeled gpu flavors keep generic requests generic", cache: gpuQueueCache(h100Flavor, l4Flavor), clusterQueue: "gpu-cq", wantCarol: "nvidia.com/gpu"},
		{name: "labeled and unlabeled gpu flavors keep generic requests generic", cache: gpuQueueCache(h100Flavor, createTestResourceFlavor("default-flavor", nil)), clusterQueue: "gpu-cq", wantCarol: "nvidia.com/gpu"},
		{name: "queue missing from cache keeps generic requests generic", cache: gpuQueueCache(h100Flavor), clusterQueue: "other-cq", wantCarol: "nvidia.com/gpu"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateUserAllocations(workUnits, buildUniqueQueueAcceleratorMap(tc.cache, tc.clusterQueue))
			want := []*spb.UserResourceAllocation{
				{User: alice, ResourceType: "memory", PendingUnits: 32.5},
				{User: alice, ResourceType: "cpu", RunningUnits: 16},
				{User: alice, ResourceType: h100, RunningUnits: 8},
				{User: bob, ResourceType: h100, RunningUnits: 2, PendingUnits: 4},
				{User: carol, ResourceType: tc.wantCarol, RunningUnits: 4},
				{User: dave, ResourceType: "google.com/tpu: tpu-v5-lite-podslice", PendingUnits: 4},
			}
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("aggregateUserAllocations() mismatch (-want +got):\n%s", diff)
			}
		})
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
