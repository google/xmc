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

// buildQueueOverviewFromClusterQueue transforms a Kueue ClusterQueue CRD into a QueueOverview proto message.
func buildQueueOverviewFromClusterQueue(cq *kueuev1beta1.ClusterQueue) *spb.QueueOverview {
	usageMap := extractResourceTotalsFromStatus(cq.Status.FlavorsUsage)
	resMap := extractResourceTotalsFromStatus(cq.Status.FlavorsReservation)

	var capacities []*spb.QueueResourceCapacity
	for _, rg := range cq.Spec.ResourceGroups {
		for _, f := range rg.Flavors {
			flvName := string(f.Name)
			for _, res := range f.Resources {
				capMetric := buildResourceCapacity(flvName, res, usageMap, resMap)
				capacities = append(capacities, capMetric)
			}
		}
	}

	return &spb.QueueOverview{
		QueueName:     cq.Name,
		QueueCapacity: capacities,
	}
}

// buildResourceCapacity calculates nominal, used, free, and borrowing capacity for a single resource quota into a QueueResourceCapacity proto.
func buildResourceCapacity(flavorName string, res kueuev1beta1.ResourceQuota, usageMap, resMap map[string]string) *spb.QueueResourceCapacity {
	resName := string(res.Name)
	lookupKey := formatFlavorResourceKey(flavorName, resName)
	nomVal := parseK8sResourceVal(resName, res.NominalQuota.String())
	useVal := parseK8sResourceVal(resName, usageMap[lookupKey])
	resVal := parseK8sResourceVal(resName, resMap[lookupKey])

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
		borrowVal = parseK8sResourceVal(resName, res.BorrowingLimit.String())
	}

	return &spb.QueueResourceCapacity{
		ResourceType:              resName,
		TotalCapacity:             nomVal,
		FreeCapacity:              freeVal,
		FreeCapacityWithBorrowing: freeVal + borrowVal,
	}
}
