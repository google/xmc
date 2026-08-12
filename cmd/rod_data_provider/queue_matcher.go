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
	"strings"

	spb "xmanager_cloud/api/protos/rod"
)

// filterFittingQueues filters queue overviews based on requested resource requirements.
func filterFittingQueues(queues []*spb.QueueOverview, req *spb.GetQueuesRequest) []*spb.QueueOverview {
	if req == nil || len(req.Requirements) == 0 {
		return append([]*spb.QueueOverview(nil), queues...)
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
	if q == nil {
		return false
	}
	for _, req := range reqs {
		if req == nil {
			continue
		}
		if !capacitySatisfiesRequirement(q.QueueCapacity, req) {
			return false
		}
	}
	return true
}

// capacitySatisfiesRequirement checks if any resource capacity metric meets a single resource requirement.
func capacitySatisfiesRequirement(capacities []*spb.QueueResourceCapacity, req *spb.Resource) bool {
	if req == nil {
		return false
	}
	for _, c := range capacities {
		if c != nil && strings.EqualFold(c.ResourceType, req.ResourceType) && c.FreeCapacityWithBorrowing >= req.Count {
			return true
		}
	}
	return false
}
