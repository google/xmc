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
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

// QueueVelocityFetcher defines interface for fetching velocity metrics from storage.
type QueueVelocityFetcher = storage.StatusHistoryStore

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

	var rawVelocity []storage.QueueMetricRow
	var rawPending []storage.QueueMetricRow
	var queueMapping map[string][]string

	if s.velocityStore != nil && len(req.GetQueueNames()) > 0 {
		var targetQueues []string
		targetQueues, queueMapping = s.cache.GetLocalQueuesForClusterQueues(req.GetQueueNames())

		var err error
		rawVelocity, err = fetchBatchVelocity(ctx, s.velocityStore, targetQueues, windowDuration)
		if err != nil {
			return nil, err
		}
		rawPending, err = fetchBatchPendingMetrics(ctx, s.velocityStore, targetQueues)
		if err != nil {
			return nil, err
		}
	}

	velocityAgg := mergeClusterQueueMetrics(queueMapping, aggregateQueueMetricRows(rawVelocity))
	pendingAgg := mergeClusterQueueMetrics(queueMapping, aggregateQueueMetricRows(rawPending))

	queueNames := req.GetQueueNames()
	queueMap := make(map[string]*spb.ClusterQueueSecondaryMetrics, len(queueNames))
	for _, qName := range queueNames {
		vel := velocityAgg[qName]
		pending := pendingAgg[qName]
		predictabilities := buildQueuePredictabilities(preset, windowHours, vel, pending)
		queueMap[qName] = &spb.ClusterQueueSecondaryMetrics{
			QueuePredictability: predictabilities,
		}
	}

	return &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: queueMap,
	}, nil
}

type aggregatedQueueMetric struct {
	jobCount         int64
	resourceUnitsMap map[string]int64
}

func aggregateQueueMetricRows(rows []storage.QueueMetricRow) map[string]aggregatedQueueMetric {
	aggregated := make(map[string]aggregatedQueueMetric)
	for _, r := range rows {
		curr, ok := aggregated[r.QueueName]
		if !ok {
			curr = aggregatedQueueMetric{
				jobCount:         r.Count,
				resourceUnitsMap: make(map[string]int64),
			}
		}
		if r.Count > curr.jobCount {
			curr.jobCount = r.Count
		}
		if r.ResourceType != "" {
			curr.resourceUnitsMap[r.ResourceType] += r.TotalUnits
		}
		aggregated[r.QueueName] = curr
	}
	return aggregated
}

func mergeClusterQueueMetrics(clusterQueueToLocalQueueMapping map[string][]string, localQueueMetrics map[string]aggregatedQueueMetric) map[string]aggregatedQueueMetric {
	merged := make(map[string]aggregatedQueueMetric, len(clusterQueueToLocalQueueMapping))
	for cq, targets := range clusterQueueToLocalQueueMapping {
		var combined aggregatedQueueMetric
		combined.resourceUnitsMap = make(map[string]int64)
		for _, target := range targets {
			metric, ok := localQueueMetrics[target]
			if !ok {
				continue
			}
			combined.jobCount += metric.jobCount
			for resType, units := range metric.resourceUnitsMap {
				combined.resourceUnitsMap[resType] += units
			}
		}
		merged[cq] = combined
	}
	return merged
}

func buildQueuePredictabilities(preset spb.RangePreset, windowHours float64, res aggregatedQueueMetric, pendingRes aggregatedQueueMetric) []*spb.QueuePredictability {
	resourceTypes := make(map[string]bool)
	for k := range res.resourceUnitsMap {
		if k != "" {
			resourceTypes[k] = true
		}
	}
	for k := range pendingRes.resourceUnitsMap {
		if k != "" {
			resourceTypes[k] = true
		}
	}

	jobVel := float64(res.jobCount) / windowHours

	if len(resourceTypes) == 0 {
		return []*spb.QueuePredictability{
			{
				VelocityRange: preset,
				JobVelocity:   jobVel,
				PendingJobs:   pendingRes.jobCount,
			},
		}
	}

	sortedTypes := make([]string, 0, len(resourceTypes))
	for rType := range resourceTypes {
		sortedTypes = append(sortedTypes, rType)
	}
	sort.Strings(sortedTypes)

	predictabilities := make([]*spb.QueuePredictability, 0, len(sortedTypes))
	for _, rType := range sortedTypes {
		units := res.resourceUnitsMap[rType]
		velVal := float64(units)
		if strings.EqualFold(rType, constants.ResourceTypeCPU) {
			velVal /= 1000.0
		}

		pUnits := pendingRes.resourceUnitsMap[rType]
		pVal := float64(pUnits)
		if strings.EqualFold(rType, constants.ResourceTypeCPU) {
			pVal /= 1000.0
		}

		predictabilities = append(predictabilities, &spb.QueuePredictability{
			VelocityRange: preset,
			JobVelocity:   jobVel,
			ResourceVelocity: &spb.ResourceVelocity{
				ResourceType: rType,
				Velocity:     velVal / windowHours,
			},
			PendingResourceRequests: &spb.Resource{
				ResourceType: rType,
				Count:        pVal,
			},
			PendingJobs: pendingRes.jobCount,
		})
	}

	return predictabilities
}

func fetchBatchVelocity(ctx context.Context, store QueueVelocityFetcher, queueNames []string, windowDuration time.Duration) ([]storage.QueueMetricRow, error) {
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

func fetchBatchPendingMetrics(ctx context.Context, store QueueVelocityFetcher, queueNames []string) ([]storage.QueueMetricRow, error) {
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
