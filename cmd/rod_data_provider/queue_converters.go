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

type resourceAggregator struct {
	totalNominal   float64
	totalUsed      float64
	totalBorrowing float64
}

// buildQueueOverviewFromClusterQueue transforms a Kueue ClusterQueue CRD into a QueueOverview proto message,
// aggregating capacities across all configured resource flavors per distinct resource type.
func buildQueueOverviewFromClusterQueue(cq *kueuev1beta1.ClusterQueue) *spb.QueueOverview {
	usageMap := extractResourceTotalsFromStatus(cq.Status.FlavorsUsage)
	resMap := extractResourceTotalsFromStatus(cq.Status.FlavorsReservation)

	aggMap, resourceOrder := accumulateClusterQueueCapacities(cq, usageMap, resMap)

	var capacities []*spb.QueueResourceCapacity
	for _, resName := range resourceOrder {
		capMetric := buildQueueResourceCapacity(resName, aggMap[resName])
		capacities = append(capacities, capMetric)
	}

	return &spb.QueueOverview{
		QueueName:     cq.Name,
		QueueCapacity: capacities,
	}
}

// accumulateClusterQueueCapacities aggregates nominal, used, and borrowing quotas across all flavors in a ClusterQueue.
func accumulateClusterQueueCapacities(cq *kueuev1beta1.ClusterQueue, usageMap, resMap map[string]string) (map[string]*resourceAggregator, []string) {
	aggMap := make(map[string]*resourceAggregator)
	var resourceOrder []string

	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			flvName := string(f.Name)
			for _, res := range f.Resources {
				resName := string(res.Name)
				if _, exists := aggMap[resName]; !exists {
					aggMap[resName] = &resourceAggregator{}
					resourceOrder = append(resourceOrder, resName)
				}
				accumulateResourceQuota(flvName, res, usageMap, resMap, aggMap[resName])
			}
		}
	}
	return aggMap, resourceOrder
}

// accumulateResourceQuota parses and adds a single resource quota's values to a resourceAggregator.
func accumulateResourceQuota(flavorName string, res kueuev1beta1.ResourceQuota, usageMap, resMap map[string]string, agg *resourceAggregator) {
	resName := string(res.Name)
	lookupKey := formatFlavorResourceKey(flavorName, resName)

	nomVal := parseK8sResourceVal(resName, res.NominalQuota.String())
	useVal := parseK8sResourceVal(resName, usageMap[lookupKey])
	resVal := parseK8sResourceVal(resName, resMap[lookupKey])

	usedVal := resVal
	if useVal > usedVal {
		usedVal = useVal
	}

	borrowVal := 0.0
	if res.BorrowingLimit != nil {
		borrowVal = parseK8sResourceVal(resName, res.BorrowingLimit.String())
	}

	agg.totalNominal += nomVal
	agg.totalUsed += usedVal
	agg.totalBorrowing += borrowVal
}

// buildQueueResourceCapacity converts an aggregated resourceAggregator into a QueueResourceCapacity proto.
func buildQueueResourceCapacity(resName string, agg *resourceAggregator) *spb.QueueResourceCapacity {
	freeVal := agg.totalNominal - agg.totalUsed
	if freeVal < 0 {
		freeVal = 0
	}
	return &spb.QueueResourceCapacity{
		ResourceType:              resName,
		TotalCapacity:             agg.totalNominal,
		FreeCapacity:              freeVal,
		FreeCapacityWithBorrowing: freeVal + agg.totalBorrowing,
	}
}
