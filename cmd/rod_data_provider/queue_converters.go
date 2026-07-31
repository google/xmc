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
)

// ResourceQuotaAggregator holds accumulated nominal quotas and borrowing limits for a specific resource type across all flavors.
type ResourceQuotaAggregator struct {
	nominalQuota   float64
	borrowingLimit float64
}

// buildQueueOverviewFromClusterQueue transforms a Kueue ClusterQueue CRD into a QueueOverview proto message.
// It resolves specific accelerator names (e.g. "nvidia-tesla-h100" or "nvidia-l4") using the provided ResourceFlavor map.
// It executes a clear 3-step pipeline:
//  1. Extract total used capacity per resource type from ClusterQueueStatus (summed across flavors, with accelerator resolution).
//  2. Aggregate nominal quotas and borrowing limits per resource type from ClusterQueueSpec (summed across flavors, with accelerator resolution).
//  3. Compute free capacity and construct QueueResourceCapacity proto messages for each resource type.
func buildQueueOverviewFromClusterQueue(clusterQueue *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) *spb.QueueOverview {
	if clusterQueue == nil {
		return nil
	}

	// Step 1: Extract active resource usage across all flavors from Status.
	totalUsedCapacityByResourceType := extractTotalResourceUsageByResourceType(clusterQueue.Status, rfMap)

	// Step 2: Aggregate nominal quotas and borrowing limits across all flavors from Spec.
	quotaAggregatorByResourceType, orderedResourceTypes := aggregateClusterQueueNominalAndBorrowingLimits(clusterQueue, rfMap)

	// Step 3: Compute free capacities and build proto messages.
	var queueCapacities []*spb.QueueResourceCapacity
	for _, resourceType := range orderedResourceTypes {
		quotaAggregator := quotaAggregatorByResourceType[resourceType]
		usedCapacity := totalUsedCapacityByResourceType[resourceType]
		capacityProto := convertAggregatedQuotaToResourceCapacity(resourceType, quotaAggregator, usedCapacity)
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
func aggregateClusterQueueNominalAndBorrowingLimits(clusterQueue *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) (map[string]*ResourceQuotaAggregator, []string) {
	quotaAggregatorByResourceType := make(map[string]*ResourceQuotaAggregator)
	var orderedResourceTypes []string

	for _, resourceGroup := range clusterQueue.Spec.ResourceGroups {
		for _, flavorQuota := range resourceGroup.Flavors {
			flavorName := string(flavorQuota.Name)
			for _, resourceQuota := range flavorQuota.Resources {
				rawResourceType := string(resourceQuota.Name)
				resolvedType := resolveResourceType(rawResourceType, flavorName, rfMap)

				if _, exists := quotaAggregatorByResourceType[resolvedType]; !exists {
					quotaAggregatorByResourceType[resolvedType] = &ResourceQuotaAggregator{}
					orderedResourceTypes = append(orderedResourceTypes, resolvedType)
				}
				addFlavorResourceQuotaToAggregator(resourceQuota, quotaAggregatorByResourceType[resolvedType])
			}
		}
	}
	return quotaAggregatorByResourceType, orderedResourceTypes
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
func convertAggregatedQuotaToResourceCapacity(resourceType string, quotaAggregator *ResourceQuotaAggregator, totalUsedCapacity float64) *spb.QueueResourceCapacity {
	freeCapacity := quotaAggregator.nominalQuota - totalUsedCapacity
	if freeCapacity < 0 {
		freeCapacity = 0
	}
	freeCapacityWithBorrowing := (quotaAggregator.nominalQuota + quotaAggregator.borrowingLimit) - totalUsedCapacity
	if freeCapacityWithBorrowing < 0 {
		freeCapacityWithBorrowing = 0
	}
	return &spb.QueueResourceCapacity{
		ResourceType:              resourceType,
		TotalCapacity:             quotaAggregator.nominalQuota,
		FreeCapacity:              freeCapacity,
		FreeCapacityWithBorrowing: freeCapacityWithBorrowing,
	}
}
