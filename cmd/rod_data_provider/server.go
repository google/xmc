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

// WorkUnitFetcher defines storage operations required to fetch active jobs by queue name.
type WorkUnitFetcher interface {
	FetchActiveWorkUnitsByQueue(ctx context.Context, queueName string, pageSize int32, pageToken string) ([]*spb.JobInfo, string, error)
}

type server struct {
	spb.UnimplementedResourceObservabilityServiceServer
	cfg           *Config
	workUnitStore WorkUnitFetcher
}

func newServer(cfg *Config) *server {
	return &server{cfg: cfg}
}

func newServerWithFetcher(cfg *Config, fetcher WorkUnitFetcher) *server {
	return &server{
		cfg:           cfg,
		workUnitStore: fetcher,
	}
}

// GetFittingQueues returns ClusterQueues matching requested resource requirements.
func (s *server) GetFittingQueues(ctx context.Context, req *spb.GetFittingQueuesRequest) (*spb.GetFittingQueuesResponse, error) {
	queues := []*spb.QueueOverview{
		{
			QueueName: "cluster-queue-a",
			QueueCapacity: []*spb.QueueResourceCapacity{
				{
					ResourceType:              "cpu",
					TotalCapacity:             100,
					FreeCapacity:              40,
					FreeCapacityWithBorrowing: 60,
				},
				{
					ResourceType:              "memory",
					TotalCapacity:             500,
					FreeCapacity:              200,
					FreeCapacityWithBorrowing: 300,
				},
			},
		},
		{
			QueueName: "cluster-queue-b",
			QueueCapacity: []*spb.QueueResourceCapacity{
				{
					ResourceType:              "nvidia.com/gpu",
					TotalCapacity:             8,
					FreeCapacity:              2,
					FreeCapacityWithBorrowing: 4,
				},
			},
		},
	}

	return &spb.GetFittingQueuesResponse{
		Queues: queues,
	}, nil
}
