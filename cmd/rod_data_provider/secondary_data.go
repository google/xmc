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
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
)

// QueueVelocityFetcher defines interface for fetching velocity metrics from storage.
type QueueVelocityFetcher interface{}

// BatchGetQueueSecondaryData handles calculation and retrieval of queue velocity metrics.
// UI-Driven PR 1 Mock Implementation: returns hardcoded predictability response per queue.
func (s *server) BatchGetQueueSecondaryData(ctx context.Context, req *spb.BatchGetQueueSecondaryDataRequest) (*spb.BatchGetQueueSecondaryDataResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}

	preset := req.GetRange()
	queueMap := make(map[string]*spb.ClusterQueueSecondaryMetrics)
	for _, qName := range req.GetQueueNames() {
		resType := "CPU"
		if strings.Contains(strings.ToLower(qName), "gpu") {
			resType = "GPU H100"
		} else if strings.Contains(strings.ToLower(qName), "tpu") {
			resType = "TPU v5p"
		}
		pred := &spb.QueuePredictability{
			VelocityRange: preset,
			JobVelocity:   12.5,
			ResourceVelocity: &spb.ResourceVelocity{
				ResourceType: resType,
				Velocity:     25.0,
			},
			PendingJobs: 3,
			PendingResourceRequests: &spb.Resource{
				ResourceType: resType,
				Count:        6,
			},
		}
		queueMap[qName] = &spb.ClusterQueueSecondaryMetrics{
			QueuePredictability: []*spb.QueuePredictability{pred},
		}
	}

	return &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: queueMap,
	}, nil
}
