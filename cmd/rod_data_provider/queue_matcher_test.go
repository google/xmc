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

	tests := []struct {
		name      string
		req       *spb.GetQueuesRequest
		wantCount int
		wantQueue string
	}{
		{
			name:      "All Queues Without Filter",
			req:       &spb.GetQueuesRequest{},
			wantCount: 2,
		},
		{
			name: "Filter By Resource Requirement",
			req: &spb.GetQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 2},
				},
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Filter Out When Resource Requirement Exceeds Free Capacity",
			req: &spb.GetQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 10},
				},
			},
			wantCount: 0,
		},
		{
			name: "Case-Insensitive Resource Type Matching",
			req: &spb.GetQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "CPU", Count: 12},
				},
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Multi-Resource Filter Requirement Satisfies Both GPU and CPU",
			req: &spb.GetQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 4},
					{ResourceType: "cpu", Count: 16},
				},
			},
			wantCount: 1,
			wantQueue: "cq-gpu-1",
		},
		{
			name: "Multi-Resource Filter Requirement Fails When One Resource Exceeds Capacity",
			req: &spb.GetQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "nvidia.com/gpu", Count: 4},
					{ResourceType: "cpu", Count: 32},
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
			if tt.wantQueue != "" && (len(got) == 0 || got[0].QueueName != tt.wantQueue) {
				var gotQueue string
				if len(got) > 0 {
					gotQueue = got[0].QueueName
				}
				t.Errorf("filterFittingQueues queue name = %q, want %q", gotQueue, tt.wantQueue)
			}
		})
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

	// Verify slice immutability when request is nil/empty
	if len(resultQueues) > 0 {
		resultQueues[0] = &spb.QueueOverview{QueueName: "mutated"}
	}
	if inputQueues[0].QueueName != "q1" {
		t.Errorf("filterFittingQueues bypassed slice immutability; original slice mutated")
	}

	// Test nil safety with nil elements inside slices
	queuesWithNil := []*spb.QueueOverview{
		nil,
		{
			QueueName: "q2",
			QueueCapacity: []*spb.QueueResourceCapacity{
				nil,
				{ResourceType: "cpu", FreeCapacityWithBorrowing: 4},
			},
		},
	}
	reqWithNil := &spb.GetQueuesRequest{
		Requirements: []*spb.Resource{
			nil,
			{ResourceType: "cpu", Count: 2},
		},
	}
	got := filterFittingQueues(queuesWithNil, reqWithNil)
	if len(got) != 1 || got[0].QueueName != "q2" {
		t.Errorf("filterFittingQueues with nil elements failed, got: %v", got)
	}
}
