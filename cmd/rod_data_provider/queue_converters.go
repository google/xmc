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
	totalBorrowing float64
}

// buildQueueOverviewFromClusterQueue transforms a Kueue ClusterQueue CRD into a QueueOverview proto message,
// aggregating capacities across all configured resource flavors per distinct resource type.
func buildQueueOverviewFromClusterQueue(cq *kueuev1beta1.ClusterQueue) *spb.QueueOverview {
	usedMap := extractStatusUsageTotals(cq.Status)
	aggMap, resourceOrder := accumulateClusterQueueQuotas(cq)

	var capacities []*spb.QueueResourceCapacity
	for _, resName := range resourceOrder {
		capMetric := buildQueueResourceCapacity(resName, aggMap[resName], usedMap[resName])
		capacities = append(capacities, capMetric)
	}

	return &spb.QueueOverview{
		QueueName:     cq.Name,
		QueueCapacity: capacities,
	}
}

// accumulateClusterQueueQuotas sums nominal quotas and borrowing limits across all flavors per resource type.
func accumulateClusterQueueQuotas(cq *kueuev1beta1.ClusterQueue) (map[string]*resourceAggregator, []string) {
	aggMap := make(map[string]*resourceAggregator)
	var resourceOrder []string

	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			for _, res := range f.Resources {
				resName := string(res.Name)
				if _, exists := aggMap[resName]; !exists {
					aggMap[resName] = &resourceAggregator{}
					resourceOrder = append(resourceOrder, resName)
				}
				accumulateResourceQuota(res, aggMap[resName])
			}
		}
	}
	return aggMap, resourceOrder
}

// accumulateResourceQuota parses and adds a single resource quota's nominal and borrowing limits to a resourceAggregator.
func accumulateResourceQuota(res kueuev1beta1.ResourceQuota, agg *resourceAggregator) {
	resName := string(res.Name)
	agg.totalNominal += parseK8sResourceVal(resName, res.NominalQuota.String())

	if res.BorrowingLimit != nil {
		agg.totalBorrowing += parseK8sResourceVal(resName, res.BorrowingLimit.String())
	}
}

// buildQueueResourceCapacity converts an aggregated resourceAggregator and totalUsed value into a QueueResourceCapacity proto.
func buildQueueResourceCapacity(resName string, agg *resourceAggregator, totalUsed float64) *spb.QueueResourceCapacity {
	freeVal := agg.totalNominal - totalUsed
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
