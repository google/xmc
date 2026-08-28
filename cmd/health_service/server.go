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
	"os"
	"sync"
	"time"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/logging/logging"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
	guard   AdminRoleGuard

	// Caching
	cacheMu      sync.RWMutex
	cachedStatus *spb.HealthStatusResponse
	cacheExpires time.Time

	// Rate limiting & Singleflight
	forceRefreshLimiter *rate.Limiter
	sfGroup             singleflight.Group
}

func newServer(cfg *Config, checker healthChecker, guard AdminRoleGuard) *server {
	var limiter *rate.Limiter
	if cfg.ForceRefreshMaxRequests > 0 && cfg.ForceRefreshWindow > 0 {
		limiter = rate.NewLimiter(rate.Limit(float64(cfg.ForceRefreshMaxRequests)/cfg.ForceRefreshWindow.Seconds()), cfg.ForceRefreshMaxRequests)
	}
	return &server{
		cfg:                 cfg,
		checker:             checker,
		guard:               guard,
		forceRefreshLimiter: limiter,
	}
}

func (s *server) Close() error {
	return s.checker.Close()
}

func (s *server) GetHealthStatus(ctx context.Context, req *spb.GetHealthStatusRequest) (*spb.HealthStatusResponse, error) {
	if err := s.guard.CheckAdminRole(ctx); err != nil {
		return nil, err
	}

	forceRefresh := req.GetForceRefresh()
	if forceRefresh && s.forceRefreshLimiter != nil && !s.forceRefreshLimiter.Allow() {
		logging.Debug(ctx, "rate limit exceeded for force refresh, ignoring flag and falling back to cache")
		forceRefresh = false
	}

	if !forceRefresh {
		if cached := s.getHealthStatusCache(); cached != nil {
			_ = grpc.SetHeader(ctx, metadata.Pairs("x-cache", "HIT"))
			return cached, nil
		}
	}

	_ = grpc.SetHeader(ctx, metadata.Pairs("x-cache", "MISS"))
	return s.runChecksSingleflight(ctx)
}

func (s *server) runChecksSingleflight(ctx context.Context) (*spb.HealthStatusResponse, error) {
	ch := s.sfGroup.DoChan("health_status", func() (val interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = status.Errorf(codes.Internal, "panic in health check: %v", r)
			}
		}()
		detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		resp := s.calculateHealthStatus(detachedCtx)
		s.updateHealthStatusCache(resp)
		return resp, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		resp, ok := res.Val.(*spb.HealthStatusResponse)
		if !ok || resp == nil {
			return nil, status.Error(codes.Internal, "failed to calculate health status")
		}
		return proto.Clone(resp).(*spb.HealthStatusResponse), nil
	}
}

func (s *server) calculateHealthStatus(ctx context.Context) *spb.HealthStatusResponse {
	res := s.checker.RunChecks(ctx)

	overallHealthy := spb.HealthState_HEALTH_STATE_HEALTHY
	for _, c := range res.Components {
		if c.HealthState != spb.HealthState_HEALTH_STATE_HEALTHY {
			overallHealthy = spb.HealthState_HEALTH_STATE_UNHEALTHY
			logging.Warn(ctx, "Component unhealthy", "name", c.Name, "error", c.ErrorMessage)
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
		HealthCheckTime: timestamppb.Now(),
	}
}

func (s *server) getHealthStatusCache() *spb.HealthStatusResponse {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if s.cachedStatus != nil && time.Now().Before(s.cacheExpires) {
		return proto.Clone(s.cachedStatus).(*spb.HealthStatusResponse)
	}
	return nil
}

func (s *server) updateHealthStatusCache(resp *spb.HealthStatusResponse) {
	if resp == nil {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cachedStatus = resp
	s.cacheExpires = time.Now().Add(s.cfg.CacheTTL)
}

func (s *server) ListConfigEntries(ctx context.Context, req *spb.ListConfigEntriesRequest) (*spb.ListConfigEntriesResponse, error) {
	if err := s.guard.CheckAdminRole(ctx); err != nil {
		return nil, err
	}

	groups, err := listConfigs(ctx, os.DirFS(s.cfg.ConfigDir))
	if err != nil {
		return nil, err
	}
	return &spb.ListConfigEntriesResponse{
		ConfigGroups: groups,
	}, nil
}

func (s *server) GetUserInfo(ctx context.Context, req *spb.GetUserInfoRequest) (*spb.UserInfoResponse, error) {
	email, hasRole, err := s.guard.HasAdminRole(ctx)
	if err != nil {
		logging.Error(ctx, "Failed to check admin role for user info", "error", err)
		return &spb.UserInfoResponse{UserEmail: "", Privileges: nil}, nil
	}

	var privileges []spb.Privilege
	if hasRole {
		privileges = append(privileges, spb.Privilege_PRIVILEGE_ACCESS_ADMIN_PANEL)
	}

	return &spb.UserInfoResponse{
		UserEmail:  email,
		Privileges: privileges,
	}, nil
}
