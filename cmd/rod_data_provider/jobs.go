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
	// TODO: replace with actual implementation
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil || req.GetQueueName() == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_name is required")
	}

	jobs := mockDashboardJobs()
	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:           jobs,
		TotalQueueSize: int64(len(jobs)),
	}, nil
}

func mockDashboardJobs() []*spb.DashboardJob {
	return []*spb.DashboardJob{
		{
			Xid:             "53296",
			WorkUnitId:      1,
			User:            "example@example.com",
			Priority:        0,
			Status:          spb.WorkUnitState_RUNNING,
			PositionInQueue: 0,
		},
		{
			Xid:             "53308",
			WorkUnitId:      2,
			User:            "example-admin@example.com",
			Priority:        100,
			Status:          spb.WorkUnitState_SCHEDULING,
			PositionInQueue: 1,
		},
	}
}
