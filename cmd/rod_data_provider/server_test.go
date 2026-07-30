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
	"testing"

	spb "xmanager_cloud/api/protos/rod"
)

func TestGetFittingQueues(t *testing.T) {
	ctx := context.Background()
	cache := NewKueueCache()

	cq1 := createTestClusterQueueTyped("cluster-queue-a", "default-cohort", "BestEffortFIFO", "8", "4", "2")
	cq2 := createTestClusterQueueTyped("cluster-queue-b", "gpu-cohort", "StrictFIFO", "16", "8", "4")

	cache.SetClusterQueue(cq1)
	cache.SetClusterQueue(cq2)

	cfg := &Config{
		Port:     "8080",
		GRPCPort: "50051",
	}
	srv := newServer(cfg, cache)

	tests := []struct {
		name      string
		req       *spb.GetFittingQueuesRequest
		wantCount int32
	}{
		{
			name: "Returns Matching Fitting Queues",
			req: &spb.GetFittingQueuesRequest{
				ResourceRequirements: []*spb.ResourceRequirement{
					{ResourceName: "nvidia.com/gpu", Quantity: "2"},
				},
			},
			wantCount: 2,
		},
		{
			name: "Filter By Pattern",
			req: &spb.GetFittingQueuesRequest{
				QueueNamePattern: "queue-a",
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.GetFittingQueues(ctx, tt.req)
			if err != nil {
				t.Fatalf("GetFittingQueues failed: %v", err)
			}
			if resp.TotalCount != tt.wantCount {
				t.Errorf("TotalCount = %d, want %d", resp.TotalCount, tt.wantCount)
			}
			if len(resp.ClusterQueues) != int(tt.wantCount) {
				t.Errorf("ClusterQueues len = %d, want %d", len(resp.ClusterQueues), tt.wantCount)
			}
		})
	}
}
