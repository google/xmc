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

	spb "xmanager_cloud/api/protos/rod"
)

type server struct {
	spb.UnimplementedResourceObservabilityServiceServer
	cfg *Config
}

func newServer(cfg *Config) *server {
	return &server{cfg: cfg}
}

// GetFittingQueues returns ClusterQueues matching requested resource requirements.
func (s *server) GetFittingQueues(ctx context.Context, req *spb.GetFittingQueuesRequest) (*spb.GetFittingQueuesResponse, error) {
	queues := []*spb.ClusterQueue{
		{
			Name:                  "cluster-queue-a",
			Strategy:              "BestEffortFIFO",
			Cohort:                "default-cohort",
			Status:                "ACTIVE",
			StatusMessage:         "ClusterQueue is active and accepting workloads",
			LastTransitionTime:    "2026-07-29T08:00:00Z",
			AdmittedWorkloads:     5,
			ReservingWorkloads:    2,
			PendingWorkloads:      1,
			PendingJobsCount:      1,
			RequestedResources:    "cpu: 2000m, memory: 4Gi",
			DominantResource:      "cpu",
			DominantResourceUnits: 20,
			AdmittedJobsCount:     5,
			ResourceVelocity:      "15.2 units/hour",
			JobVelocity:           "12 jobs/hour",
			LocalQueues: []*spb.LocalQueueRef{
				{
					Name:      "team-a-queue",
					Namespace: "team-a",
				},
			},
			Flavors: []*spb.ResourceFlavorMetric{
				{
					Name:         "default-flavor",
					ResourceName: "cpu",
					NominalQuota: "100",
					InUse:        "40",
					Reserved:     "20",
					FreeCapacity: "40",
					LargestSlot:  "20",
				},
				{
					Name:         "default-flavor",
					ResourceName: "memory",
					NominalQuota: "500Gi",
					InUse:        "200Gi",
					Reserved:     "100Gi",
					FreeCapacity: "200Gi",
					LargestSlot:  "100Gi",
				},
			},
		},
		{
			Name:                  "cluster-queue-b",
			Strategy:              "StrictFIFO",
			Cohort:                "gpu-cohort",
			Status:                "ACTIVE",
			StatusMessage:         "ClusterQueue is active and accepting workloads",
			LastTransitionTime:    "2026-07-29T08:00:00Z",
			AdmittedWorkloads:     2,
			ReservingWorkloads:    1,
			PendingWorkloads:      0,
			PendingJobsCount:      0,
			RequestedResources:    "nvidia.com/gpu: 2",
			DominantResource:      "nvidia.com/gpu",
			DominantResourceUnits: 4,
			AdmittedJobsCount:     2,
			ResourceVelocity:      "5.0 units/hour",
			JobVelocity:           "4 jobs/hour",
			LocalQueues: []*spb.LocalQueueRef{
				{
					Name:      "gpu-queue",
					Namespace: "default",
				},
			},
			Flavors: []*spb.ResourceFlavorMetric{
				{
					Name:         "gpu-flavor",
					ResourceName: "nvidia.com/gpu",
					NominalQuota: "8",
					InUse:        "4",
					Reserved:     "2",
					FreeCapacity: "2",
					LargestSlot:  "2",
				},
			},
		},
	}

	return &spb.GetFittingQueuesResponse{
		ClusterQueues: queues,
	}, nil
}
