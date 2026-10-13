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
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// GetRunningAndPendingJobs handles fetching active and pending jobs for a cluster queue.
func (s *server) GetRunningAndPendingJobs(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest) (*spb.GetRunningAndPendingJobsResponse, error) {
	if err := validateJobsRequest(ctx, req, s.workUnitStore); err != nil {
		return nil, err
	}

	clusterQueueName := req.GetQueueName()

	targetQueues := s.cache.GetLocalQueuesForClusterQueue(clusterQueueName)

	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 10
	}
	pageSize = min(pageSize, 100)

	result, err := s.workUnitStore.FetchActiveWorkUnitsByQueues(ctx, targetQueues, pageSize, req.GetPageToken())
	if err != nil {
		logging.Error(ctx, "failed to fetch active work units from storage", "cluster_queue_name", req.GetQueueName(), "target_queues", targetQueues, "error", err)
		if errors.Is(err, storage.ErrInvalidPageToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		return nil, status.Error(codes.Internal, "failed to fetch active jobs")
	}

	workUnits, nextPageToken := extractWorkUnitsResult(result)
	jobs := mapWorkUnitsToDashboardJobs(ctx, workUnits, time.Now())

	var totalQueueSize int64
	if result != nil {
		totalQueueSize = result.TotalQueueSize
	}

	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:           jobs,
		NextPageToken:  nextPageToken,
		TotalQueueSize: totalQueueSize,
	}, nil
}

func validateJobsRequest(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest, store WorkUnitFetcher) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if req == nil || req.GetQueueName() == "" {
		return status.Error(codes.InvalidArgument, "queue_name is required")
	}
	if req.GetPageSize() < 0 {
		return status.Error(codes.InvalidArgument, "page_size cannot be negative")
	}
	if store == nil {
		return status.Error(codes.Unavailable, "work unit store is uninitialized")
	}
	return nil
}

func extractWorkUnitsResult(result *storage.ActiveWorkUnitsResult) (workUnits []storage.WorkUnitWithAuthor, nextPageToken string) {
	if result == nil {
		return nil, ""
	}
	return result.WorkUnits, result.NextPageToken
}

func mapWorkUnitsToDashboardJobs(ctx context.Context, workUnits []storage.WorkUnitWithAuthor, now time.Time) []*spb.DashboardJob {
	var jobs []*spb.DashboardJob
	for _, item := range workUnits {
		if job := mapWorkUnitToDashboardJob(ctx, item.WorkUnit, item.Author, now); job != nil {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// mapWorkUnitToDashboardJob transforms an essmsgpb.WorkUnit domain entity into a spb.DashboardJob presentation DTO.
func mapWorkUnitToDashboardJob(ctx context.Context, wu *essmsgpb.WorkUnit, user string, now time.Time) *spb.DashboardJob {
	if wu == nil {
		return nil
	}
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		logging.Warn(ctx, "invalid work unit name", "name", wu.GetName(), "error", err)
		return nil
	}
	return &spb.DashboardJob{
		Xid:                xid,
		WorkUnitId:         wid,
		User:               user,
		Priority:           int64(wu.GetPriority()),
		Status:             extractWorkUnitState(wu),
		TimeInQueueSeconds: calculateQueueDuration(wu, now),
		RequestedResources: extractRequestedResources(wu),
		PositionInQueue:    extractQueuePosition(wu),
		CreateTime:         wu.GetCreateTime(),
		Tags:               wu.GetTags(),
	}
}

func extractWorkUnitState(wu *essmsgpb.WorkUnit) essmsgpb.WorkUnitStatus_WorkUnitState {
	if exec := wu.GetCurrentWorkUnitExecution(); exec != nil && exec.GetStatus() != nil {
		return exec.GetStatus().GetState()
	}
	return essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED
}

func extractQueuePosition(wu *essmsgpb.WorkUnit) int64 {
	exec := wu.GetCurrentWorkUnitExecution()
	if exec == nil || exec.GetStatus() == nil || exec.GetStatus().GetSchedulingInformation() == nil {
		return -1
	}
	aux := exec.GetStatus().GetSchedulingInformation().GetAuxiliaryInfo()
	if aux == nil || aux.GetQueueInfo() == nil {
		return -1
	}
	return int64(aux.GetQueueInfo().GetPositionInQueue())
}

func calculateQueueDuration(wu *essmsgpb.WorkUnit, now time.Time) int64 {
	createTime := wu.GetCreateTime()
	if createTime == nil || !createTime.IsValid() {
		return 0
	}
	diff := int64(now.Sub(createTime.AsTime()).Seconds())
	if diff > 0 {
		return diff
	}
	return 0
}
