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
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func buildResourceFlavorMap(c *cache.KueueCache) map[string]*kueuev1beta1.ResourceFlavor {
	if c != nil && c.ResourceFlavors != nil {
		flavors := c.ResourceFlavors.List()
		rfMap := make(map[string]*kueuev1beta1.ResourceFlavor, len(flavors))
		for _, rf := range flavors {
			if rf != nil {
				rfMap[rf.Name] = rf
			}
		}
		return rfMap
	}
	return make(map[string]*kueuev1beta1.ResourceFlavor)
}

func buildQueueOverviews(c *cache.KueueCache, rfMap map[string]*kueuev1beta1.ResourceFlavor) []*spb.QueueOverview {
	if c == nil || c.ClusterQueues == nil {
		return nil
	}
	clusterQueues := c.ClusterQueues.List()
	cohortCapacitiesMap := computeCohortCapacities(clusterQueues, rfMap)

	overviews := make([]*spb.QueueOverview, 0, len(clusterQueues))
	for _, cq := range clusterQueues {
		if cq == nil {
			continue
		}
		var cohortCapacities *CohortResourceCapacities
		if cq.Spec.Cohort != "" {
			cohortCapacities = cohortCapacitiesMap[string(cq.Spec.Cohort)]
		}
		if overview := buildQueueOverviewFromClusterQueue(cq, rfMap, cohortCapacities); overview != nil {
			overviews = append(overviews, overview)
		}
	}
	return overviews
}

// CohortResourceQuota holds accumulated nominal quota and used capacity for a specific resource type within a cohort.
type CohortResourceQuota struct {
	NominalQuota float64
	UsedCapacity float64
}

// FreeCapacity returns max(0, NominalQuota - UsedCapacity).
func (q *CohortResourceQuota) FreeCapacity() float64 {
	if q == nil {
		return 0
	}
	free := q.NominalQuota - q.UsedCapacity
	if free < 0 {
		return 0
	}
	return free
}

// CohortResourceCapacities holds aggregated resource quotas per resource type for a cohort.
type CohortResourceCapacities struct {
	quotas map[string]*CohortResourceQuota
}

// FreeCapacityFor returns the available free capacity for a given resource type in this cohort.
func (c *CohortResourceCapacities) FreeCapacityFor(resourceType string) *float64 {
	if c == nil {
		return nil
	}
	q, exists := c.quotas[resourceType]
	if !exists {
		return nil
	}
	free := q.FreeCapacity()
	return &free
}

func (c *CohortResourceCapacities) getOrCreateQuota(resourceType string) *CohortResourceQuota {
	if c.quotas == nil {
		c.quotas = make(map[string]*CohortResourceQuota)
	}
	if c.quotas[resourceType] == nil {
		c.quotas[resourceType] = &CohortResourceQuota{}
	}
	return c.quotas[resourceType]
}

// computeCohortCapacities calculates aggregated nominal quotas and used capacities across all ClusterQueues for each cohort.
func computeCohortCapacities(clusterQueues []*kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) map[string]*CohortResourceCapacities {
	cohortCapacitiesMap := make(map[string]*CohortResourceCapacities)

	for _, cq := range clusterQueues {
		if cq == nil || cq.Spec.Cohort == "" {
			continue
		}
		cohortName := string(cq.Spec.Cohort)
		if cohortCapacitiesMap[cohortName] == nil {
			cohortCapacitiesMap[cohortName] = &CohortResourceCapacities{}
		}
		cohortCapacities := cohortCapacitiesMap[cohortName]

		totalUsedCapacityByResourceType := extractTotalResourceUsageByResourceType(cq.Status, rfMap)
		for resourceType, used := range totalUsedCapacityByResourceType {
			quota := cohortCapacities.getOrCreateQuota(resourceType)
			quota.UsedCapacity += used
		}

		aggregatedQuotas := aggregateClusterQueueNominalAndBorrowingLimits(cq, rfMap)
		for resourceType, quotaAgg := range aggregatedQuotas.aggregators {
			if quotaAgg != nil {
				quota := cohortCapacities.getOrCreateQuota(resourceType)
				quota.NominalQuota += quotaAgg.nominalQuota
			}
		}
	}

	return cohortCapacitiesMap
}

// ResourceQuotaAggregator holds accumulated nominal quotas and borrowing limits for a specific resource type across all flavors.
type ResourceQuotaAggregator struct {
	nominalQuota   float64
	borrowingLimit float64
}

// aggregatedClusterQueueQuotas holds aggregated quotas per resource type and preserves insertion order.
type aggregatedClusterQueueQuotas struct {
	aggregators  map[string]*ResourceQuotaAggregator
	orderedTypes []string
}

// buildQueueOverviewFromClusterQueue transforms a Kueue ClusterQueue CRD into a QueueOverview proto message.
// It resolves specific accelerator names (e.g. "nvidia-tesla-h100" or "nvidia-l4") using the provided ResourceFlavor map.
// It executes a clear 3-step pipeline:
//  1. Extract total used capacity per resource type from ClusterQueueStatus (summed across flavors, with accelerator resolution).
//  2. Aggregate nominal quotas and borrowing limits per resource type from ClusterQueueSpec (summed across flavors, with accelerator resolution).
//  3. Compute free capacity and construct QueueResourceCapacity proto messages for each resource type (clamping FreeCapacityWithBorrowing to available cohort capacity if present).
func buildQueueOverviewFromClusterQueue(clusterQueue *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor, cohortCapacities *CohortResourceCapacities) *spb.QueueOverview {
	if clusterQueue == nil {
		return nil
	}

	// Step 1: Extract active resource usage across all flavors from Status.
	totalUsedCapacityByResourceType := extractTotalResourceUsageByResourceType(clusterQueue.Status, rfMap)

	// Step 2: Aggregate nominal quotas and borrowing limits across all flavors from Spec.
	aggregatedQuotas := aggregateClusterQueueNominalAndBorrowingLimits(clusterQueue, rfMap)

	// Step 3: Compute free capacities and build proto messages.
	var queueCapacities []*spb.QueueResourceCapacity
	for _, resourceType := range aggregatedQuotas.orderedTypes {
		quotaAggregator := aggregatedQuotas.aggregators[resourceType]
		usedCapacity := totalUsedCapacityByResourceType[resourceType]

		var cohortFree *float64
		if clusterQueue.Spec.Cohort != "" && cohortCapacities != nil {
			cohortFree = cohortCapacities.FreeCapacityFor(resourceType)
		}

		capacityProto := convertAggregatedQuotaToResourceCapacity(resourceType, quotaAggregator, usedCapacity, cohortFree)
		queueCapacities = append(queueCapacities, capacityProto)
	}

	return &spb.QueueOverview{
		QueueName:     clusterQueue.Name,
		QueueCapacity: queueCapacities,
	}
}

// aggregateClusterQueueNominalAndBorrowingLimits iterates over all resource groups and flavors in ClusterQueueSpec,
// summing nominal quotas and borrowing limits for each distinct resource type across all flavors.
// Generic accelerator names (e.g. "nvidia.com/gpu") are resolved to specific accelerator types (e.g. "nvidia-tesla-h100") using rfMap.
func aggregateClusterQueueNominalAndBorrowingLimits(clusterQueue *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) *aggregatedClusterQueueQuotas {
	result := &aggregatedClusterQueueQuotas{
		aggregators: make(map[string]*ResourceQuotaAggregator),
	}

	for _, resourceGroup := range clusterQueue.Spec.ResourceGroups {
		for _, flavorQuota := range resourceGroup.Flavors {
			flavorName := string(flavorQuota.Name)
			for _, resourceQuota := range flavorQuota.Resources {
				rawResourceType := string(resourceQuota.Name)
				resolvedType := resolveResourceType(rawResourceType, flavorName, rfMap)

				if _, exists := result.aggregators[resolvedType]; !exists {
					result.aggregators[resolvedType] = &ResourceQuotaAggregator{}
					result.orderedTypes = append(result.orderedTypes, resolvedType)
				}
				addFlavorResourceQuotaToAggregator(resourceQuota, result.aggregators[resolvedType])
			}
		}
	}
	return result
}

// addFlavorResourceQuotaToAggregator parses a single flavor's ResourceQuota and adds its nominal quota and borrowing limit to the aggregator.
func addFlavorResourceQuotaToAggregator(resourceQuota kueuev1beta1.ResourceQuota, aggregator *ResourceQuotaAggregator) {
	resourceType := string(resourceQuota.Name)
	aggregator.nominalQuota += parseK8sResourceQuantity(resourceType, resourceQuota.NominalQuota)

	if resourceQuota.BorrowingLimit != nil {
		aggregator.borrowingLimit += parseK8sResourceQuantity(resourceType, *resourceQuota.BorrowingLimit)
	}
}

// convertAggregatedQuotaToResourceCapacity combines an aggregated ResourceQuotaAggregator and used capacity into a QueueResourceCapacity proto.
// If cohortFree is non-nil, FreeCapacityWithBorrowing is capped at *cohortFree (min(LocalFree, CohortFree)).
func convertAggregatedQuotaToResourceCapacity(resourceType string, quotaAggregator *ResourceQuotaAggregator, totalUsedCapacity float64, cohortFree *float64) *spb.QueueResourceCapacity {
	var nominalQuota, borrowingLimit float64
	if quotaAggregator != nil {
		nominalQuota = quotaAggregator.nominalQuota
		borrowingLimit = quotaAggregator.borrowingLimit
	}
	freeCapacity := nominalQuota - totalUsedCapacity
	if freeCapacity < 0 {
		freeCapacity = 0
	}
	freeCapacityWithBorrowing := (nominalQuota + borrowingLimit) - totalUsedCapacity
	if freeCapacityWithBorrowing < 0 {
		freeCapacityWithBorrowing = 0
	}
	if cohortFree != nil {
		if *cohortFree < freeCapacityWithBorrowing {
			freeCapacityWithBorrowing = *cohortFree
		}
	}
	return &spb.QueueResourceCapacity{
		ResourceType:              resourceType,
		TotalCapacity:             nominalQuota,
		FreeCapacity:              freeCapacity,
		FreeCapacityWithBorrowing: freeCapacityWithBorrowing,
	}
}
