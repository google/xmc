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
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

// GetSubmitQueue returns the recommended target submission queue (LocalQueue) for a cluster queue.
func (s *server) GetSubmitQueue(ctx context.Context, req *spb.GetSubmitQueueRequest) (*spb.GetSubmitQueueResponse, error) {
	if err := validateSubmitQueueRequest(ctx, req, s.cache); err != nil {
		return nil, err
	}

	submitQueueName, err := selectSubmitQueue(s.cache, req.GetQueueName())
	if err != nil {
		return nil, err
	}

	return &spb.GetSubmitQueueResponse{
		SubmitQueue: submitQueueName,
	}, nil
}

func validateSubmitQueueRequest(ctx context.Context, req *spb.GetSubmitQueueRequest, c *cache.KueueCache) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if req == nil || req.GetQueueName() == "" {
		return status.Error(codes.InvalidArgument, "queue_name is required")
	}
	if c == nil || c.LocalQueues == nil {
		return status.Error(codes.Unavailable, "kueue cache is uninitialized")
	}
	return nil
}

func selectSubmitQueue(c *cache.KueueCache, clusterQueueName string) (string, error) {
	localQueues := c.LocalQueues.List()
	var bestLQ *kueuev1beta1.LocalQueue
	var minScheduledJobs int64 = -1

	for _, lq := range localQueues {
		if lq == nil || string(lq.Spec.ClusterQueue) != clusterQueueName {
			continue
		}
		scheduledJobs := int64(lq.Status.PendingWorkloads) + int64(lq.Status.AdmittedWorkloads)
		if bestLQ == nil || scheduledJobs < minScheduledJobs || (scheduledJobs == minScheduledJobs && queueKey(lq) < queueKey(bestLQ)) {
			bestLQ = lq
			minScheduledJobs = scheduledJobs
		}
	}

	if bestLQ == nil {
		return "", status.Errorf(codes.NotFound, "no local queue found for cluster queue %q", clusterQueueName)
	}

	return bestLQ.Name, nil
}

func queueKey(lq *kueuev1beta1.LocalQueue) string {
	if lq == nil {
		return ""
	}
	if lq.Namespace == "" {
		return lq.Name
	}
	return lq.Namespace + "/" + lq.Name
}
