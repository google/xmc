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
	if s.cache == nil || s.cache.ClusterQueues == nil {
		return &spb.GetFittingQueuesResponse{}, nil
	}

	// Step 1: Build ResourceFlavor map from cached ResourceFlavor CRDs.
	rfMap := make(map[string]*kueuev1beta1.ResourceFlavor)
	if s.cache.ResourceFlavors != nil {
		for _, rf := range s.cache.ResourceFlavors.List() {
			if rf != nil {
				rfMap[rf.Name] = rf
			}
		}
	}

	// Step 2: Convert cached ClusterQueue CRDs into QueueOverview proto messages.
	clusterQueues := s.cache.ClusterQueues.List()
	overviews := make([]*spb.QueueOverview, 0, len(clusterQueues))
	for _, cq := range clusterQueues {
		if cq == nil {
			continue
		}
		overview := buildQueueOverviewFromClusterQueue(cq, rfMap)
		if overview != nil {
			overviews = append(overviews, overview)
		}
	}

	// Step 3: Filter fitting queues based on request requirements.
	fittingQueues := filterFittingQueues(overviews, req)

	return &spb.GetFittingQueuesResponse{
		Queues: fittingQueues,
	}, nil
}
