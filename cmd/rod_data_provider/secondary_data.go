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
		batchResults, err = fetchBatchVelocity(ctx, s.velocityStore, req.GetQueueNames(), windowDuration)
		if err != nil {
			return nil, err
		}
	}

	queueMap := make(map[string]*spb.ClusterQueueSecondaryMetrics)
	for _, qName := range req.GetQueueNames() {
		res := batchResults[qName]
		predictability := buildQueuePredictability(preset, windowHours, res)
		queueMap[qName] = &spb.ClusterQueueSecondaryMetrics{
			QueuePredictability: []*spb.QueuePredictability{predictability},
		}
	}

	return &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: queueMap,
	}, nil
}

func buildQueuePredictability(preset spb.RangePreset, windowHours float64, res statushistory.QueueVelocityResult) *spb.QueuePredictability {
	jobVel := float64(res.AdmittedJobsCount) / windowHours
	resType := "CPU"
	if res.ResourceType != "" {
		resType = res.ResourceType
	}
	return &spb.QueuePredictability{
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
}

func fetchBatchVelocity(ctx context.Context, store QueueVelocityFetcher, queueNames []string, windowDuration time.Duration) (map[string]statushistory.QueueVelocityResult, error) {
	batchResults, err := store.FetchBatchQueueVelocity(ctx, queueNames, windowDuration)
	if err == nil {
		return batchResults, nil
	}

	logging.Warn(ctx, "failed to batch fetch queue velocity", "error", err)
	if errors.Is(err, context.Canceled) {
		return nil, status.Error(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, status.Error(codes.DeadlineExceeded, err.Error())
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, status.FromContextError(ctxErr).Err()
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		return nil, err
	}
	return nil, status.Errorf(codes.Internal, "failed to fetch batch queue velocity: %v", err)
}

func rangePresetToDuration(preset spb.RangePreset) time.Duration {
	switch preset {
	case spb.RangePreset_RANGE_PRESET_LAST_DAY:
		return 24 * time.Hour
	case spb.RangePreset_RANGE_PRESET_LAST_WEEK:
		return 7 * 24 * time.Hour
	case spb.RangePreset_RANGE_PRESET_LAST_HOUR:
		fallthrough
	default:
		return 1 * time.Hour
	}
}
