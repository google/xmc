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

type server struct {
	spb.UnimplementedResourceObservabilityServiceServer
	cfg   *Config
	cache *cache.KueueCache
}

func newServer(cfg *Config, cache *cache.KueueCache) *server {
	return &server{
		cfg:   cfg,
		cache: cache,
	}
}

// GetFittingQueues returns ClusterQueues matching requested resource requirements directly from in-memory KueueCache.
func (s *server) GetFittingQueues(ctx context.Context, req *spb.GetFittingQueuesRequest) (*spb.GetFittingQueuesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if s.cache == nil || s.cache.ClusterQueues == nil {
		return nil, status.Error(codes.Unavailable, "kueue cache is uninitialized")
	}

	rfMap := buildResourceFlavorMap(s.cache)
	overviews := buildQueueOverviews(s.cache, rfMap)
	fittingQueues := filterFittingQueues(overviews, req)

	return &spb.GetFittingQueuesResponse{
		Queues: fittingQueues,
	}, nil
}

func buildResourceFlavorMap(c *cache.KueueCache) map[string]*kueuev1beta1.ResourceFlavor {
	rfMap := make(map[string]*kueuev1beta1.ResourceFlavor)
	if c != nil && c.ResourceFlavors != nil {
		for _, rf := range c.ResourceFlavors.List() {
			if rf != nil {
				rfMap[rf.Name] = rf
			}
		}
	}
	return rfMap
}

func buildQueueOverviews(c *cache.KueueCache, rfMap map[string]*kueuev1beta1.ResourceFlavor) []*spb.QueueOverview {
	if c == nil || c.ClusterQueues == nil {
		return nil
	}
	clusterQueues := c.ClusterQueues.List()
	overviews := make([]*spb.QueueOverview, 0, len(clusterQueues))
	for _, cq := range clusterQueues {
		if cq == nil {
			continue
		}
		if overview := buildQueueOverviewFromClusterQueue(cq, rfMap); overview != nil {
			overviews = append(overviews, overview)
		}
	}
	return overviews
}
