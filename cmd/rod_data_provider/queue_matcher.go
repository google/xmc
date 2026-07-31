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
	"strings"

	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// FetchClusterQueues queries ClusterQueues directly from the in-memory KueueCache with zero network List calls.
func FetchClusterQueues(ctx context.Context, cache *cache.KueueCache, req *spb.GetFittingQueuesRequest) ([]*spb.QueueOverview, error) {
	if cache == nil {
		return []*spb.QueueOverview{}, nil
	}

	cqList := cache.ClusterQueues.List()

	var results []*spb.QueueOverview
	for _, cq := range cqList {
		if cq == nil {
			continue
		}
		overview := buildQueueOverviewFromClusterQueue(cq)
		results = append(results, overview)
	}

	return filterFittingQueues(results, req), nil
}

// filterFittingQueues filters queue overviews based on requested resource requirements.
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

// satisfiesRequirements checks if a QueueOverview satisfies all requested resource requirements.
func satisfiesRequirements(q *spb.QueueOverview, reqs []*spb.Resource) bool {
	for _, req := range reqs {
		if !capacitySatisfiesRequirement(q.QueueCapacity, req) {
			return false
		}
	}
	return true
}

// capacitySatisfiesRequirement checks if any resource capacity metric meets a single resource requirement.
func capacitySatisfiesRequirement(capacities []*spb.QueueResourceCapacity, req *spb.Resource) bool {
	for _, cap := range capacities {
		if strings.EqualFold(cap.ResourceType, req.ResourceType) {
			if cap.FreeCapacityWithBorrowing >= req.Count {
				return true
			}
		}
	}
	return false
}
