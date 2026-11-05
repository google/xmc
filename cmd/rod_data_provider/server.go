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
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/internal/storage"
)

// WorkUnitFetcher defines storage operations required to fetch active jobs by queue name.
type WorkUnitFetcher interface {
	FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*storage.ActiveWorkUnitsResult, error)
	AggregateActiveResourcesByAuthor(ctx context.Context, queueNames []string) ([]storage.AuthorResourceAllocation, error)
	WithReadOnlyWorkUnitSnapshot(ctx context.Context, fn func(ctx context.Context, repo storage.WorkUnitSnapshotRepository) error) error
}

type server struct {
	spb.UnimplementedResourceObservabilityServiceServer
	cfg           *Config
	cache         *cache.KueueCache
	workUnitStore WorkUnitFetcher
	velocityStore QueueVelocityFetcher
}

var _ spb.ResourceObservabilityServiceServer = (*server)(nil)

func newServer(cfg *Config, cache *cache.KueueCache, fetcher WorkUnitFetcher, velocityFetcher QueueVelocityFetcher) *server {
	return &server{
		cfg:           cfg,
		cache:         cache,
		workUnitStore: fetcher,
		velocityStore: velocityFetcher,
	}
}

// ListClusterQueues returns ClusterQueues matching requested resource requirements directly from in-memory KueueCache.
func (s *server) ListClusterQueues(ctx context.Context, req *spb.ListClusterQueuesRequest) (*spb.ListClusterQueuesResponse, error) {
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

	return &spb.ListClusterQueuesResponse{
		Queues: overviews,
	}, nil
}
