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
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
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

	workUnits, nextPageToken, err := s.workUnitStore.FetchActiveWorkUnitsByQueue(ctx, queueName, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch active jobs: %v", err)
	}

	var jobs []*spb.JobInfo
	positionCounter := 1

	for _, wu := range workUnits {
		if wu == nil {
			continue
		}
		state := getExecutionState(wu)

		posStr := "N/A-running"
		if state == "INITIALIZING" || state == "SCHEDULING" {
			posStr = strconv.Itoa(positionCounter)
			positionCounter++
		}

		if jobInfo := mapWorkUnitToJobInfo(wu, posStr, queueName); jobInfo != nil {
			jobs = append(jobs, jobInfo)
		}
	}

	return &spb.GetRunningAndPendingJobsResponse{
		QueueName:     queueName,
		Jobs:          jobs,
		NextPageToken: nextPageToken,
	}, nil
}

// getExecutionState extracts execution state string safely from a WorkUnit domain entity.
func getExecutionState(wu *essmsgpb.WorkUnit) string {
	if wu == nil || wu.GetCurrentWorkUnitExecution() == nil || wu.GetCurrentWorkUnitExecution().GetStatus() == nil {
		return ""
	}
	return wu.GetCurrentWorkUnitExecution().GetStatus().GetState().String()
}

// formatPriority formats integer or proto enum priority to a human-readable display string.
func formatPriority(prio int64) string {
	switch prio {
	case int64(essmsgpb.Priority_HIGH):
		return "High"
	case int64(essmsgpb.Priority_LOW):
		return "Low"
	case int64(essmsgpb.Priority_NORMAL):
		return "Normal"
	default:
		if prio >= 200 {
			return "High"
		}
		if prio <= 50 {
			return "Low"
		}
		return "Normal"
	}
}

// formatTimeInQueue calculates human-readable duration string since creation.
func formatTimeInQueue(t time.Time) string {
	duration := time.Since(t)
	if duration < time.Minute {
		return "just now"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}

// mapWorkUnitToJobInfo transforms an essmsgpb.WorkUnit domain entity into a rod JobInfo presentation DTO.
func mapWorkUnitToJobInfo(wu *essmsgpb.WorkUnit, positionStr string, queueName string) *spb.JobInfo {
	if wu == nil {
		return nil
	}
	xid, wid, _ := validator.ValidateWorkUnitName(wu.GetName())
	xidStr := strconv.FormatInt(xid, 10)
	widStr := strconv.FormatInt(wid, 10)

	state := getExecutionState(wu)

	var createTime time.Time
	var createTimeStr string
	if wu.GetCreateTime() != nil {
		createTime = wu.GetCreateTime().AsTime()
		createTimeStr = createTime.Format(time.RFC3339)
	}

	timeInQueue := ""
	if !createTime.IsZero() {
		timeInQueue = formatTimeInQueue(createTime)
	}

	return &spb.JobInfo{
		Position:     positionStr,
		Xid:          xidStr,
		WorkUnitId:   widStr,
		JobName:      fmt.Sprintf("xmc-operation-%d-%d", xid, wid),
		User:         "",
		Resources:    "CPU: 1",
		Priority:     formatPriority(int64(wu.GetPriority())),
		Status:       state,
		TimeInQueue:  timeInQueue,
		QueueName:    queueName,
		CreationTime: createTimeStr,
	}
}
