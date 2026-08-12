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
	"xmanager_cloud/internal/storage/spanner/statushistory"
	"xmanager_cloud/logging/logging"
)

// QueueVelocityFetcher defines interface for fetching velocity metrics from storage.
type QueueVelocityFetcher interface {
	FetchQueueVelocity(ctx context.Context, queueName string, window time.Duration) (statushistory.QueueVelocityResult, error)
	FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) (map[string]statushistory.QueueVelocityResult, error)
}

// BatchGetQueueSecondaryData handles calculation and retrieval of queue velocity metrics.
func (s *server) BatchGetQueueSecondaryData(ctx context.Context, req *spb.BatchGetQueueSecondaryDataRequest) (*spb.BatchGetQueueSecondaryDataResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}

	preset := req.GetRange()
	windowDuration := rangePresetToDuration(preset)
	windowHours := windowDuration.Hours()
	if windowHours <= 0 {
		windowHours = 1.0
	}

	var batchResults map[string]statushistory.QueueVelocityResult
	if s.velocityStore != nil && len(req.GetQueueNames()) > 0 {
		var err error
		batchResults, err = s.velocityStore.FetchBatchQueueVelocity(ctx, req.GetQueueNames(), windowDuration)
		if err != nil {
			logging.Warn(ctx, "failed to batch fetch queue velocity", "error", err)
		}
	}

	queueMap := make(map[string]*spb.QueueSecondaryData)
	for _, qName := range req.GetQueueNames() {
		res := batchResults[qName]
		jobVel := float64(res.AdmittedJobsCount) / windowHours
		resType := "CPU"
		if res.ResourceType != "" {
			resType = res.ResourceType
		}
		predictability := &spb.QueuePredictability{
			VelocityRange: preset,
			JobVelocity:   jobVel,
			ResourceVelocity: &spb.ResourceVelocity{
				ResourceType: resType,
				Velocity:     jobVel * float64(res.ResourceUnits),
			},
			PendingResourceRequests: &spb.Resource{
				ResourceType: resType,
				Count:        0,
			},
			PendingJobs: 0,
		}

		queueMap[qName] = &spb.QueueSecondaryData{
			QueuePredictability: []*spb.QueuePredictability{predictability},
		}
	}

	return &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: queueMap,
	}, nil
}

func rangePresetToDuration(preset spb.RangePreset) time.Duration {
	switch preset {
	case spb.RangePreset_LAST_DAY:
		return 24 * time.Hour
	case spb.RangePreset_LAST_WEEK:
		return 7 * 24 * time.Hour
	case spb.RangePreset_LAST_HOUR:
		fallthrough
	default:
		return 1 * time.Hour
	}
}
