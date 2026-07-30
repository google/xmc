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

	spb "xmanager_cloud/api/protos/rod"
)

type server struct {
	spb.UnimplementedResourceObservabilityServiceServer
	cfg   *Config
	cache *KueueCache
}

func newServer(cfg *Config, cache *KueueCache) *server {
	return &server{
		cfg:   cfg,
		cache: cache,
	}
}

// GetFittingQueues returns ClusterQueues matching requested resource requirements directly from in-memory KueueCache.
func (s *server) GetFittingQueues(ctx context.Context, req *spb.GetFittingQueuesRequest) (*spb.GetFittingQueuesResponse, error) {
	queues, err := FetchClusterQueues(ctx, s.cache, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster queues: %w", err)
	}

	return &spb.GetFittingQueuesResponse{
		ClusterQueues: queues,
		TotalCount:    int32(len(queues)),
	}, nil
}
