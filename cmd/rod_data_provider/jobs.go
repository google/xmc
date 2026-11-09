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
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	spb "xmanager_cloud/api/protos/rod"
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

	result, activeResources, err := fetchJobsAndAllocations(ctx, s.workUnitStore, targetQueues, pageSize, req.GetPageToken())
	if err != nil {
		logging.Error(ctx, "failed to fetch active jobs from storage", "cluster_queue_name", req.GetQueueName(), "target_queues", targetQueues, "error", err)
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

	uniqueAccelerators := buildUniqueQueueAcceleratorMap(s.cache, clusterQueueName)
	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:            jobs,
		NextPageToken:   nextPageToken,
		TotalQueueSize:  totalQueueSize,
		UserAllocations: aggregateUserAllocations(activeResources, uniqueAccelerators),
	}, nil
}

func fetchJobsAndAllocations(ctx context.Context, store WorkUnitFetcher, targetQueues []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, []storage.ActiveWorkUnitResources, error) {
	result, err := store.FetchActiveWorkUnitsByQueues(ctx, targetQueues, pageSize, pageToken)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch active work units: %w", err)
	}
	activeResources, err := store.FetchActiveWorkUnitResources(ctx, targetQueues)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch active work unit resources: %w", err)
	}
	return result, activeResources, nil
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

func extractWorkUnitsResult(result *storage.ActiveWorkUnitsResult) (workUnits []storage.ActiveWorkUnit, nextPageToken string) {
	if result == nil {
		return nil, ""
	}
	return result.WorkUnits, result.NextPageToken
}

func mapWorkUnitsToDashboardJobs(ctx context.Context, workUnits []storage.ActiveWorkUnit, now time.Time) []*spb.DashboardJob {
	var jobs []*spb.DashboardJob
	for _, item := range workUnits {
		if job := mapWorkUnitToDashboardJob(ctx, item, now); job != nil {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// mapWorkUnitToDashboardJob transforms a storage.ActiveWorkUnit domain entity into a spb.DashboardJob presentation DTO.
func mapWorkUnitToDashboardJob(ctx context.Context, item storage.ActiveWorkUnit, now time.Time) *spb.DashboardJob {
	wu := item.WorkUnit
	if wu == nil {
		return nil
	}
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		logging.Warn(ctx, "invalid work unit name", "name", wu.GetName(), "error", err)
		return nil
	}
	return &spb.DashboardJob{
		Xid:                    xid,
		WorkUnitId:             wid,
		User:                   item.Author,
		Priority:               int64(wu.GetPriority()),
		Status:                 extractWorkUnitState(wu),
		TimeInQueueSeconds:     calculateQueueDuration(wu, now),
		RunningDurationSeconds: calculateRunningDuration(wu, now),
		RequestedResources:     extractRequestedResources(wu),
		PositionInQueue:        extractQueuePosition(wu),
		CreateTime:             wu.GetCreateTime(),
		Tags:                   wu.GetTags(),
		ExperimentTitle:        item.ExperimentTitle,
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
	return elapsedSecondsSince(wu.GetCreateTime(), now)
}

func calculateRunningDuration(wu *essmsgpb.WorkUnit, now time.Time) int64 {
	status := wu.GetCurrentWorkUnitExecution().GetStatus()
	if status.GetState() != essmsgpb.WorkUnitStatus_RUNNING {
		return 0
	}
	return elapsedSecondsSince(status.GetStateUpdateTime(), now)
}

func elapsedSecondsSince(ts *timestamppb.Timestamp, now time.Time) int64 {
	if ts == nil || !ts.IsValid() {
		return 0
	}
	return max(int64(now.Sub(ts.AsTime()).Seconds()), 0)
}
