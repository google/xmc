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
	FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) (map[string]statushistory.QueueVelocityResult, error)
	FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) (map[string]statushistory.PendingQueueMetricsResult, error)
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
	var pendingResults map[string]statushistory.PendingQueueMetricsResult
	if s.velocityStore != nil && len(req.GetQueueNames()) > 0 {
		targetQueues, queueMapping := s.buildTargetQueueMappings(req.GetQueueNames())

		var err error
		rawVelocity, err := fetchBatchVelocity(ctx, s.velocityStore, targetQueues, windowDuration)
		if err != nil {
			return nil, err
		}
		rawPending, err := fetchBatchPendingMetrics(ctx, s.velocityStore, targetQueues)
		if err != nil {
			return nil, err
		}

		batchResults = mergeVelocityResults(queueMapping, rawVelocity)
		pendingResults = mergePendingResults(queueMapping, rawPending)
	}

	queueMap := make(map[string]*spb.ClusterQueueSecondaryMetrics)
	for _, qName := range req.GetQueueNames() {
		res := batchResults[qName]
		pendingRes := pendingResults[qName]
		predictability := buildQueuePredictability(preset, windowHours, res, pendingRes)
		queueMap[qName] = &spb.ClusterQueueSecondaryMetrics{
			QueuePredictability: []*spb.QueuePredictability{predictability},
		}
	}

	return &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: queueMap,
	}, nil
}

func (s *server) buildTargetQueueMappings(queueNames []string) ([]string, map[string][]string) {
	var targetQueues []string
	queueMapping := make(map[string][]string)
	for _, cq := range queueNames {
		qs := []string{cq}
		if s.cache != nil {
			if lqs := s.cache.GetLocalQueuesForClusterQueue(cq); len(lqs) > 0 {
				qs = append(qs, lqs...)
			}
		}
		queueMapping[cq] = qs
		targetQueues = append(targetQueues, qs...)
	}
	return targetQueues, queueMapping
}

func mergeResourceUnitsMap(combined map[string]int64, unitsMap map[string]int64, fallbackType string, fallbackUnits int64) {
	if len(unitsMap) > 0 {
		for k, v := range unitsMap {
			combined[k] += v
		}
		return
	}
	if fallbackType != "" {
		combined[fallbackType] += fallbackUnits
	}
}

//nolint:dupl
func mergeVelocityResults(mapping map[string][]string, raw map[string]statushistory.QueueVelocityResult) map[string]statushistory.QueueVelocityResult {
	merged := make(map[string]statushistory.QueueVelocityResult)
	for cq, targets := range mapping {
		var combined statushistory.QueueVelocityResult
		combined.ResourceUnitsMap = make(map[string]int64)
		for _, t := range targets {
			r, ok := raw[t]
			if !ok {
				continue
			}
			if r.AdmittedJobsCount > combined.AdmittedJobsCount {
				combined.AdmittedJobsCount = r.AdmittedJobsCount
			}
			mergeResourceUnitsMap(combined.ResourceUnitsMap, r.ResourceUnitsMap, r.ResourceType, r.ResourceUnits)
			if combined.ResourceType == "" || combined.ResourceType == "CPU" || (r.ResourceType != "" && r.ResourceType != "CPU") {
				combined.ResourceType = r.ResourceType
				combined.ResourceUnits = combined.ResourceUnitsMap[r.ResourceType]
			}
		}
		merged[cq] = combined
	}
	return merged
}

//nolint:dupl
func mergePendingResults(mapping map[string][]string, raw map[string]statushistory.PendingQueueMetricsResult) map[string]statushistory.PendingQueueMetricsResult {
	merged := make(map[string]statushistory.PendingQueueMetricsResult)
	for cq, targets := range mapping {
		var combined statushistory.PendingQueueMetricsResult
		combined.ResourceUnitsMap = make(map[string]int64)
		for _, t := range targets {
			r, ok := raw[t]
			if !ok {
				continue
			}
			if r.PendingJobs > combined.PendingJobs {
				combined.PendingJobs = r.PendingJobs
			}
			mergeResourceUnitsMap(combined.ResourceUnitsMap, r.ResourceUnitsMap, r.ResourceType, r.PendingResourceRequests)
			if combined.ResourceType == "" || combined.ResourceType == "CPU" || (r.ResourceType != "" && r.ResourceType != "CPU") {
				combined.ResourceType = r.ResourceType
				combined.PendingResourceRequests = combined.ResourceUnitsMap[r.ResourceType]
			}
		}
		merged[cq] = combined
	}
	return merged
}

func buildQueuePredictability(preset spb.RangePreset, windowHours float64, res statushistory.QueueVelocityResult, pendingRes statushistory.PendingQueueMetricsResult) *spb.QueuePredictability {
	jobVel := float64(res.AdmittedJobsCount) / windowHours
	resType := ""
	if res.ResourceType != "" {
		resType = res.ResourceType
	} else if pendingRes.ResourceType != "" {
		resType = pendingRes.ResourceType
	}
	resVel := float64(res.ResourceUnits)
	pendingCount := float64(pendingRes.PendingResourceRequests)
	if resType == "CPU" {
		resVel /= 1000.0
		pendingCount /= 1000.0
	}
	return &spb.QueuePredictability{
		VelocityRange: preset,
		JobVelocity:   jobVel,
		ResourceVelocity: &spb.ResourceVelocity{
			ResourceType: resType,
			Velocity:     resVel / windowHours,
		},
		PendingResourceRequests: &spb.Resource{
			ResourceType: resType,
			Count:        pendingCount,
		},
		PendingJobs: pendingRes.PendingJobs,
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

func fetchBatchPendingMetrics(ctx context.Context, store QueueVelocityFetcher, queueNames []string) (map[string]statushistory.PendingQueueMetricsResult, error) {
	batchResults, err := store.FetchBatchPendingQueueMetrics(ctx, queueNames)
	if err == nil {
		return batchResults, nil
	}

	logging.Warn(ctx, "failed to batch fetch pending queue metrics", "error", err)
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
	return nil, status.Errorf(codes.Internal, "failed to fetch batch pending queue metrics: %v", err)
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
