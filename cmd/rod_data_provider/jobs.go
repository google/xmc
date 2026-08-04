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
)

// GetRunningAndPendingJobs handles fetching active and pending jobs for a cluster queue.
func (s *server) GetRunningAndPendingJobs(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest) (*spb.GetRunningAndPendingJobsResponse, error) {
	if req == nil || req.GetQueueName() == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_name is required")
	}

	queueName := req.GetQueueName()

	if s.workUnitStore == nil {
		// Mock fallback for development environment without live Spanner client
		return &spb.GetRunningAndPendingJobsResponse{
			QueueName: queueName,
			Jobs: []*spb.JobInfo{
				{
					Position:    "N/A-running",
					Xid:         "53296",
					WorkUnitId:  "1",
					JobName:     "xmc-operation-53296-1",
					User:        "example@example.com",
					Resources:   "GPU: 8, CPU: 32",
					Priority:    "Low",
					Status:      "Running",
					TimeInQueue: "10m ago",
					QueueName:   queueName,
				},
				{
					Position:    "N/A-running",
					Xid:         "53308",
					WorkUnitId:  "1",
					JobName:     "xmc-operation-53308-1",
					User:        "example@example.com",
					Resources:   "GPU: 8, CPU: 32",
					Priority:    "Low",
					Status:      "Running",
					TimeInQueue: "5m ago",
					QueueName:   queueName,
				},
			},
		}, nil
	}

	jobs, nextPageToken, err := s.workUnitStore.FetchActiveWorkUnitsByQueue(ctx, queueName, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch active jobs: %v", err)
	}

	return &spb.GetRunningAndPendingJobsResponse{
		QueueName:     queueName,
		Jobs:          jobs,
		NextPageToken: nextPageToken,
	}, nil
}
