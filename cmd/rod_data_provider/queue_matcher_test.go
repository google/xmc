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
	cq1 := createTestClusterQueueTyped("cq-gpu-1", "gpu-cohort", "StrictFIFO", "8", "4", "2")
	cq2 := createTestClusterQueueTyped("cq-cpu-2", "cpu-cohort", "BestEffortFIFO", "0", "0", "0")

	overviews := []*spb.QueueOverview{
		buildQueueOverviewFromClusterQueue(cq1, nil),
		buildQueueOverviewFromClusterQueue(cq2, nil),
	}

	tests := []struct {
		name      string
		req       *spb.GetFittingQueuesRequest
		wantCount int
		wantQueue string
	}{
		{
			name:      "All Queues Without Filter",
			req:       &spb.GetFittingQueuesRequest{},
			wantCount: 2,
		},
		{
			name: "Filter By Resource Requirement",
			req: &spb.GetFittingQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 2},
				},
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Filter Out When Resource Requirement Exceeds Free Capacity",
			req: &spb.GetFittingQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 10},
				},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterFittingQueues(overviews, tt.req)
			if len(got) != tt.wantCount {
				t.Errorf("filterFittingQueues count = %d, want %d", len(got), tt.wantCount)
			}
			if tt.wantQueue != "" && len(got) > 0 && got[0].QueueName != tt.wantQueue {
				t.Errorf("filterFittingQueues queue name = %s, want %s", got[0].QueueName, tt.wantQueue)
			}
		})
	}
}

func TestFilterFittingQueues_NilAndEmpty(t *testing.T) {
	if got := filterFittingQueues(nil, nil); len(got) != 0 {
		t.Errorf("filterFittingQueues(nil, nil) count = %d, want 0", len(got))
	}

	queue := &spb.QueueOverview{QueueName: "q1"}
	if got := filterFittingQueues([]*spb.QueueOverview{queue}, nil); len(got) != 1 {
		t.Errorf("filterFittingQueues([q1], nil) count = %d, want 1", len(got))
	}
}
