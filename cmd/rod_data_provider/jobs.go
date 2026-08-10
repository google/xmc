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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
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

	// TODO: Fetch all LocalQueues connected to a given ClusterQueue
	clusterQueueName := req.GetQueueName()

	targetQueues := []string{clusterQueueName}

	workUnits, nextPageToken, err := s.workUnitStore.FetchActiveWorkUnitsByQueues(ctx, targetQueues, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		logging.Error(ctx, "failed to fetch active work units from storage", "cluster_queue_name", clusterQueueName, "target_queues", targetQueues, "error", err)
		return nil, status.Error(codes.Internal, "failed to fetch active jobs")
	}

	now := time.Now()
	var jobs []*spb.DashboardJob
	for _, wu := range workUnits {
		if job := mapWorkUnitToDashboardJob(ctx, wu, now); job != nil {
			jobs = append(jobs, job)
		}
	}

	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:           jobs,
		NextPageToken:  nextPageToken,
		TotalQueueSize: int64(len(jobs)),
	}, nil
}

// mapWorkUnitToDashboardJob transforms an essmsgpb.WorkUnit domain entity into a spb.DashboardJob presentation DTO.
func mapWorkUnitToDashboardJob(ctx context.Context, wu *essmsgpb.WorkUnit, now time.Time) *spb.DashboardJob {
	if wu == nil {
		return nil
	}
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		logging.Warn(ctx, "invalid work unit name", "name", wu.GetName(), "error", err)
		return nil
	}

	exec := wu.GetCurrentWorkUnitExecution()
	var state essmsgpb.WorkUnitStatus_WorkUnitState
	var posInQueue int64 = -1
	if exec != nil && exec.GetStatus() != nil {
		state = exec.GetStatus().GetState()
		if queueInfo := exec.GetStatus().GetSchedulingInformation().GetAuxiliaryInfo().GetQueueInfo(); queueInfo != nil {
			posInQueue = int64(queueInfo.GetPositionInQueue())
		}
	}

	var timeInQueueSec int64 = 0
	if wu.GetCreateTime() != nil && wu.GetCreateTime().IsValid() {
		t := wu.GetCreateTime().AsTime()
		diff := int64(now.Sub(t).Seconds())
		if diff > 0 {
			timeInQueueSec = diff
		}
	}

	user := wu.GetNotes()

	return &spb.DashboardJob{
		Xid:                xid,
		WorkUnitId:         wid,
		User:               user,
		Priority:           int64(wu.GetPriority()),
		Status:             state,
		TimeInQueueSeconds: timeInQueueSec,
		PositionInQueue:    posInQueue,
		CreateTime:         wu.GetCreateTime(),
		Tags:               wu.GetTags(),
	}
}
