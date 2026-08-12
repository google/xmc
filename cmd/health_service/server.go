// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type healthChecker interface {
	RunChecks(ctx context.Context) RunChecksResult
	Close() error
}

var _ spb.HealthServiceServer = (*server)(nil)

type server struct {
	spb.UnimplementedHealthServiceServer
	cfg     *Config
	checker healthChecker

	// Caching
	cacheMu      sync.RWMutex
	cachedStatus *spb.HealthStatusResponse
	cacheExpires time.Time

	// Rate limiting & Singleflight
	forceRefreshLimiter *rate.Limiter
	sfGroup             singleflight.Group
}

func newServer(cfg *Config, checker healthChecker) *server {
	var limiter *rate.Limiter
	if cfg.ForceRefreshMaxRequests > 0 && cfg.ForceRefreshWindow > 0 {
		limiter = rate.NewLimiter(rate.Limit(float64(cfg.ForceRefreshMaxRequests)/cfg.ForceRefreshWindow.Seconds()), cfg.ForceRefreshMaxRequests)
	}
	return &server{
		cfg:                 cfg,
		checker:             checker,
		forceRefreshLimiter: limiter,
	}
}

func (s *server) Close() error {
	return s.checker.Close()
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
	if req.GetForceRefresh() {
		if s.forceRefreshLimiter != nil && !s.forceRefreshLimiter.Allow() {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded for force refresh")
		}
		return s.runChecksSingleflight(ctx)
	}

	// Try cache
	s.cacheMu.RLock()
	cached := s.cachedStatus
	expires := s.cacheExpires
	s.cacheMu.RUnlock()

	if cached != nil && time.Now().Before(expires) {
		return cached, nil
	}

	return s.runChecksSingleflight(ctx)
}

func (s *server) runChecksSingleflight(ctx context.Context) (*spb.HealthStatusResponse, error) {
	ch := s.sfGroup.DoChan("health_status", func() (interface{}, error) {
		detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		resp := s.calculateHealthStatus(detachedCtx)
		s.updateCache(resp)
		return resp, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.Val.(*spb.HealthStatusResponse), nil
	}
}

func (s *server) calculateHealthStatus(ctx context.Context) *spb.HealthStatusResponse {
	checkStartTime := time.Now()
	res := s.checker.RunChecks(ctx)

	overallHealthy := spb.HealthState_HEALTH_STATE_HEALTHY
	for _, c := range res.Components {
		if c.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
			overallHealthy = spb.HealthState_HEALTH_STATE_UNHEALTHY
			slog.Warn("Component unhealthy", "name", c.Name, "error", c.ErrorMessage)
		}
	}

	essDbSchemaVersionStr := res.EssDbSchemaVersion
	if essDbSchemaVersionStr == "" {
		essDbSchemaVersionStr = "unknown"
	}

	xidDbSchemaVersionStr := res.XidDbSchemaVersion
	if xidDbSchemaVersionStr == "" {
		xidDbSchemaVersionStr = "unknown"
	}

	appVersionStr := s.cfg.AppVersion
	if appVersionStr == "" {
		appVersionStr = "unknown"
	}

	buildVersionStr := s.cfg.BuildVersion
	if buildVersionStr == "" {
		buildVersionStr = "unknown"
	}

	return &spb.HealthStatusResponse{
		HealthState: overallHealthy,
		SystemInfo: &spb.SystemInfo{
			AppVersion:         appVersionStr,
			BuildVersion:       buildVersionStr,
			EssDbSchemaVersion: essDbSchemaVersionStr,
			XidDbSchemaVersion: xidDbSchemaVersionStr,
			ObservabilityUrl:   s.cfg.ObservabilityURL,
			IsSchedulingPaused: false,
		},
		Components:      res.Components,
		HealthCheckTime: timestamppb.New(checkStartTime),
	}
}

func (s *server) updateCache(resp *spb.HealthStatusResponse) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	checkStartTime := resp.GetHealthCheckTime().AsTime()

	if s.cachedStatus != nil && checkStartTime.Before(s.cachedStatus.GetHealthCheckTime().AsTime()) {
		return
	}

	s.cachedStatus = resp
	s.cacheExpires = checkStartTime.Add(s.cfg.CacheTTL)
}

func (s *server) ListConfigEntries(ctx context.Context, req *spb.ListConfigEntriesRequest) (*spb.ListConfigEntriesResponse, error) {
	groups, err := listConfigs(ctx, os.DirFS(s.cfg.ConfigDir))
	if err != nil {
		return nil, err
	}
	return &spb.ListConfigEntriesResponse{
		ConfigGroups: groups,
	}, nil
}

func (s *server) GetUserInfo(ctx context.Context, req *spb.GetUserInfoRequest) (*spb.UserInfoResponse, error) {
	return &spb.UserInfoResponse{
		UserEmail: "mock-user@google.com",
		Privileges: []spb.Privilege{
			spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL,
		},
	}, nil
}
