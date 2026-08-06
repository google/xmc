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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/logging/logging"
)

// GetRunningAndPendingJobs handles fetching active and pending jobs for a cluster queue.
func (s *server) GetRunningAndPendingJobs(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest) (*spb.GetRunningAndPendingJobsResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	if req == nil || req.GetQueueName() == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_name is required")
	}

	if s.workUnitStore == nil {
		return nil, status.Error(codes.Unavailable, "work unit store is uninitialized")
	}

	clusterQueueName := req.GetQueueName()

	var targetQueues []string
	if s.cache != nil {
		targetQueues = s.cache.GetLocalQueuesForClusterQueue(clusterQueueName)
	}

	jobs, nextPageToken, err := s.workUnitStore.FetchActiveWorkUnitsByQueues(ctx, targetQueues, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		logging.Error(ctx, "failed to fetch active work units from storage", "cluster_queue_name", clusterQueueName, "target_queues", targetQueues, "error", err)
		return nil, status.Error(codes.Internal, "failed to fetch active jobs")
	}

	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:           jobs,
		NextPageToken:  nextPageToken,
		TotalQueueSize: int64(len(jobs)),
	}, nil
}
