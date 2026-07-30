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
	"context"

	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
)

// FetchClusterQueues queries ClusterQueues directly from the in-memory KueueCache with zero network List calls.
func FetchClusterQueues(ctx context.Context, cache *KueueCache, req *spb.GetFittingQueuesRequest) ([]*spb.QueueOverview, error) {
	if cache == nil {
		return []*spb.QueueOverview{}, nil
	}

	cqList := cache.ClusterQueues.List()
	rfMap := cache.ResourceFlavors.Map()

	var results []*spb.QueueOverview
	for _, cq := range cqList {
		overview := buildQueueOverviewFromTyped(cq, rfMap)
		results = append(results, overview)
	}

	return filterFittingQueues(results, req), nil
}

func buildQueueOverviewFromTyped(cq *kueuev1beta1.ClusterQueue, rfMap map[string]*kueuev1beta1.ResourceFlavor) *spb.QueueOverview {
	usageMap := extractResourceTotalsFromStatus(cq.Status.FlavorsUsage)
	resMap := extractResourceTotalsFromStatus(cq.Status.FlavorsReservation)

	var capacities []*spb.QueueResourceCapacity
	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			for _, res := range f.Resources {
				capMetric := buildResourceCapacity(res, usageMap, resMap)
				capacities = append(capacities, capMetric)
			}
		}
	}

	return &spb.QueueOverview{
		QueueName:     cq.Name,
		QueueCapacity: capacities,
	}
}

func buildResourceCapacity(res kueuev1beta1.ResourceQuota, usageMap, resMap map[string]string) *spb.QueueResourceCapacity {
	resName := string(res.Name)
	nomVal, _ := parseK8sResourceVal(res.NominalQuota.String())
	useVal := parseResourceUsageVal(usageMap[resName])
	resVal := parseResourceUsageVal(resMap[resName])

	usedVal := resVal
	if useVal > usedVal {
		usedVal = useVal
	}

	freeVal := nomVal - usedVal
	if freeVal < 0 {
		freeVal = 0
	}

	borrowVal := 0.0
	if res.BorrowingLimit != nil {
		borrowVal, _ = parseK8sResourceVal(res.BorrowingLimit.String())
	}

	return &spb.QueueResourceCapacity{
		ResourceType:              resName,
		TotalCapacity:             nomVal,
		FreeCapacity:              freeVal,
		FreeCapacityWithBorrowing: freeVal + borrowVal,
	}
}

func filterFittingQueues(queues []*spb.QueueOverview, req *spb.GetFittingQueuesRequest) []*spb.QueueOverview {
	if req == nil || len(req.Requirements) == 0 {
		return queues
	}
	var filtered []*spb.QueueOverview
	for _, q := range queues {
		if satisfiesRequirements(q, req.Requirements) {
			filtered = append(filtered, q)
		}
	}
	return filtered
}
