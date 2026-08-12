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

	spb "xmanager_cloud/api/protos/rod"
)

func TestFilterFittingQueues(t *testing.T) {
	overviews := []*spb.QueueOverview{
		{
			QueueName: "cq-gpu-1",
			QueueCapacity: []*spb.QueueResourceCapacity{
				{
					ResourceType:              "nvidia.com/gpu",
					TotalCapacity:             8.0,
					FreeCapacity:              4.0,
					FreeCapacityWithBorrowing: 6.0,
				},
				{
					ResourceType:              "cpu",
					TotalCapacity:             32.0,
					FreeCapacity:              16.0,
					FreeCapacityWithBorrowing: 16.0,
				},
			},
		},
		{
			QueueName: "cq-cpu-2",
			QueueCapacity: []*spb.QueueResourceCapacity{
				{
					ResourceType:              "cpu",
					TotalCapacity:             16.0,
					FreeCapacity:              8.0,
					FreeCapacityWithBorrowing: 8.0,
				},
			},
		},
	}

	got := filterFittingQueues(overviews, &spb.GetQueuesRequest{})
	if len(got) != 2 {
		t.Errorf("filterFittingQueues count = %d, want 2", len(got))
	}
}

func TestFilterFittingQueues_NilAndEmpty(t *testing.T) {
	if got := filterFittingQueues(nil, nil); len(got) != 0 {
		t.Errorf("filterFittingQueues(nil, nil) count = %d, want 0", len(got))
	}

	queue := &spb.QueueOverview{QueueName: "q1"}
	inputQueues := []*spb.QueueOverview{queue}
	resultQueues := filterFittingQueues(inputQueues, nil)
	if len(resultQueues) != 1 {
		t.Errorf("filterFittingQueues([q1], nil) count = %d, want 1", len(resultQueues))
	}
}
