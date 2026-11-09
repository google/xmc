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
	"cmp"
	"maps"
	"slices"

	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/state"
)

type userResourceKey struct {
	user         string
	resourceType string
}

func buildUniqueQueueAcceleratorMap(kueueCache *cache.KueueCache, clusterQueueName string) map[string]string {
	if kueueCache == nil || kueueCache.ClusterQueues == nil {
		return nil
	}
	cq, ok := kueueCache.ClusterQueues.Get(clusterQueueName)
	if !ok {
		return nil
	}
	candidates := collectQueueAcceleratorCandidates(cq, buildResourceFlavorMap(kueueCache))
	unique := make(map[string]string, len(candidates))
	for rawType, resolvedSet := range candidates {
		if len(resolvedSet) == 1 {
			for resolved := range resolvedSet {
				unique[rawType] = resolved
			}
		}
	}
	return unique
}

func collectQueueAcceleratorCandidates(cq *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) map[string]map[string]struct{} {
	candidates := make(map[string]map[string]struct{})
	forEachResolvedFlavorResource(cq, rfMap, func(rawType, resolvedType string, _ kueuev1beta1.ResourceQuota) {
		if !resourceaccumulator.IsAcceleratorResource(rawType) {
			return
		}
		if candidates[rawType] == nil {
			candidates[rawType] = make(map[string]struct{})
		}
		candidates[rawType][resolvedType] = struct{}{}
	})
	return candidates
}

// aggregateUserAllocations sums each active work unit's requested resources (canonical resource
// types; memory in GiB) per creator and resource type. STARTING and RUNNING work units are admitted
// by Kueue and count as running; SCHEDULING and INITIALIZING are not admitted yet and count as
// pending, even though the jobs table orders INITIALIZING with running jobs. A generic accelerator
// is attributed to the queue's only flavor model when uniqueAccelerators has one.
func aggregateUserAllocations(workUnits []storage.ActiveWorkUnitResources, uniqueAccelerators map[string]string) []*spb.UserResourceAllocation {
	grouped := make(map[userResourceKey]*spb.UserResourceAllocation)
	for _, wu := range workUnits {
		running := slices.Contains(state.WorkUnitAdmittedStates, wu.ExecutionState)
		for resType, units := range resourceaccumulator.ExtractNormalizedExecutableResources(wu.Executable) {
			if resolved, ok := uniqueAccelerators[resType]; ok {
				resType = resolved
			}
			entry := userAllocationEntry(grouped, wu.Creator, resType)
			if running {
				entry.RunningUnits += units
			} else {
				entry.PendingUnits += units
			}
		}
	}
	allocations := slices.Collect(maps.Values(grouped))
	slices.SortFunc(allocations, compareUserResourceAllocations)
	return allocations
}

func userAllocationEntry(grouped map[userResourceKey]*spb.UserResourceAllocation, user, resType string) *spb.UserResourceAllocation {
	key := userResourceKey{user: user, resourceType: resType}
	if entry, ok := grouped[key]; ok {
		return entry
	}
	entry := &spb.UserResourceAllocation{User: user, ResourceType: resType}
	grouped[key] = entry
	return entry
}

func compareUserResourceAllocations(a, b *spb.UserResourceAllocation) int {
	aTotal := a.GetRunningUnits() + a.GetPendingUnits()
	bTotal := b.GetRunningUnits() + b.GetPendingUnits()
	if c := cmp.Compare(bTotal, aTotal); c != 0 {
		return c
	}
	if c := cmp.Compare(b.GetRunningUnits(), a.GetRunningUnits()); c != 0 {
		return c
	}
	if c := cmp.Compare(a.GetUser(), b.GetUser()); c != 0 {
		return c
	}
	return cmp.Compare(a.GetResourceType(), b.GetResourceType())
}
