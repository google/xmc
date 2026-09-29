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
	"fmt"
	"net/http"
	"sync"
	"time"

	health "xmanager_cloud/common/health"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/summary"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type RedisPingClient interface {
	Ping(ctx context.Context) error
}

type HealthCheckerParams struct {
	Config                   *Config
	EssHealthClient          health.SpannerHealthClient
	XidHealthClient          health.SpannerHealthClient
	RedisClient              RedisPingClient
	EssExpectedSchemaVersion int64
	XidExpectedSchemaVersion int64
	ComponentsProvider       summary.CustomComponentsInfoProvider
}

type HealthChecker struct {
	cfg                      *Config
	httpClient               *http.Client
	xidConn                  *grpc.ClientConn
	essHealthClient          health.SpannerHealthClient
	xidHealthClient          health.SpannerHealthClient
	redisClient              RedisPingClient
	essExpectedSchemaVersion int64
	xidExpectedSchemaVersion int64
	componentsProvider       summary.CustomComponentsInfoProvider
}

func NewHealthChecker(params HealthCheckerParams) (*HealthChecker, error) {
	if params.Config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if params.Config.XidAddr == "" {
		return nil, fmt.Errorf("xid address is required")
	}
	conn, err := grpc.NewClient(params.Config.XidAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client for xid at %s: %w", params.Config.XidAddr, err)
	}

	return &HealthChecker{
		cfg: params.Config,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		xidConn:                  conn,
		essHealthClient:          params.EssHealthClient,
		xidHealthClient:          params.XidHealthClient,
		redisClient:              params.RedisClient,
		essExpectedSchemaVersion: params.EssExpectedSchemaVersion,
		xidExpectedSchemaVersion: params.XidExpectedSchemaVersion,
		componentsProvider:       params.ComponentsProvider,
	}, nil
}

func (c *HealthChecker) Close() error {
	if c.xidConn != nil {
		if err := c.xidConn.Close(); err != nil {
			return fmt.Errorf("failed to close xid connection: %w", err)
		}
	}
	return nil
}

type RunChecksResult struct {
	Components         []*spb.ComponentHealth
	EssDbSchemaVersion string
	XidDbSchemaVersion string
}

func (c *HealthChecker) RunChecks(ctx context.Context) RunChecksResult {
	targets := c.getCheckTargets()
	components := make([]*spb.ComponentHealth, len(targets))
	var wg sync.WaitGroup

	// 1. Run generic checks in parallel
	for i, target := range targets {
		wg.Add(1)
		go func(idx int, t checkTarget) {
			defer wg.Done()
			components[idx] = t.checkFn(ctx)
		}(i, target)
	}

	// 2. Run Spanner and custom component checks in parallel
	var essComp, xidComp *spb.ComponentHealth
	var essVersion, xidVersion string
	var customCompHealths []summary.ComponentHealth
	var customCompErr error

	spannerWgCount := 2
	if c.componentsProvider != nil {
		spannerWgCount = 3
	}
	wg.Add(spannerWgCount)

	go func() {
		defer wg.Done()
		essComp, essVersion = c.CheckSpannerESS(ctx)
	}()
	go func() {
		defer wg.Done()
		xidComp, xidVersion = c.CheckSpannerXID(ctx)
	}()
	if c.componentsProvider != nil {
		go func() {
			defer wg.Done()
			compCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			customCompHealths, customCompErr = c.componentsProvider.GetComponentsHealth(compCtx)
		}()
	}

	wg.Wait()

	// 3. Append Spanner results to the components list
	components = append(components, essComp, xidComp)

	// 4. Append custom components health if provider is configured
	if c.componentsProvider != nil {
		if customCompErr != nil {
			logging.Warn(ctx, "Failed to retrieve custom components health", "error", customCompErr)
		}
		components = appendCustomComponentHealth(components, customCompErr, customCompHealths)
	}

	return RunChecksResult{
		Components:         components,
		EssDbSchemaVersion: essVersion,
		XidDbSchemaVersion: xidVersion,
	}
}

func appendCustomComponentHealth(components []*spb.ComponentHealth, err error, healths []summary.ComponentHealth) []*spb.ComponentHealth {
	if err != nil {
		return append(components, &spb.ComponentHealth{
			Name:         "Custom Components Provider",
			Type:         spb.ComponentType_COMPONENT_TYPE_CUSTOM_COMPONENT,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("failed to list custom components: %v", err),
		})
	}
	for _, ch := range healths {
		if ch.HealthState == summary.HealthStateDisabled {
			continue
		}
		components = append(components, convertCustomComponentHealth(ch))
	}
	return components
}

func convertCustomComponentHealth(ch summary.ComponentHealth) *spb.ComponentHealth {
	var hState spb.HealthState
	var errMsg string

	switch ch.HealthState {
	case summary.HealthStateHealthy:
		hState = spb.HealthState_HEALTH_STATE_HEALTHY
		errMsg = ""
	case summary.HealthStateDisabled:
		hState = spb.HealthState_HEALTH_STATE_UNSPECIFIED
		errMsg = ch.StatusMessage
	default:
		hState = spb.HealthState_HEALTH_STATE_UNHEALTHY
		errMsg = ch.StatusMessage
	}

	return &spb.ComponentHealth{
		Name:         ch.Name,
		Type:         spb.ComponentType_COMPONENT_TYPE_CUSTOM_COMPONENT,
		HealthState:  hState,
		ErrorMessage: errMsg,
	}
}

type checkTarget struct {
	name    string
	checkFn func(ctx context.Context) *spb.ComponentHealth
}

func (c *HealthChecker) getCheckTargets() []checkTarget {
	return []checkTarget{
		{
			name: "ESS",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				return c.checkHTTPComponent(ctx, "ESS", c.cfg.EssURL, "/readyz")
			},
		},
		{
			name: "XID",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				return c.checkGRPCComponent(ctx, "XID", c.xidConn, "xmc.XidService")
			},
		},
		{
			name: "Actuator",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				return c.checkHTTPComponent(ctx, "Actuator", c.cfg.ActuatorURL, "/readyz")
			},
		},
		{
			name: "Reconciler",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				return c.checkHTTPComponent(ctx, "Reconciler", c.cfg.ReconcilerURL, "/readyz")
			},
		},
		{
			name: "Log Monitor",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				return c.checkHTTPComponent(ctx, "Log Monitor", c.cfg.LogMonitorURL, "/healthz")
			},
		},
		{
			name:    "Redis",
			checkFn: c.checkRedis,
		},
	}
}

func (c *HealthChecker) CheckSpannerESS(ctx context.Context) (*spb.ComponentHealth, string) {
	return c.checkSpannerComponent(ctx, "Spanner ESS", c.essHealthClient, c.essExpectedSchemaVersion)
}

func (c *HealthChecker) CheckSpannerXID(ctx context.Context) (*spb.ComponentHealth, string) {
	return c.checkSpannerComponent(ctx, "Spanner XID", c.xidHealthClient, c.xidExpectedSchemaVersion)
}

func (c *HealthChecker) checkHTTPComponent(ctx context.Context, name string, baseURL string, path string) *spb.ComponentHealth {
	url := fmt.Sprintf("%s%s", baseURL, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("failed to create request: %v", err),
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("http request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("unexpected http status: %s", resp.Status),
		}
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}

func (c *HealthChecker) checkGRPCComponent(ctx context.Context, name string, conn *grpc.ClientConn, serviceName string) *spb.ComponentHealth {
	if conn == nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: "grpc client connection is nil",
		}
	}

	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: serviceName})
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("grpc check call failed: %v", err),
		}
	}

	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("grpc service status: %s", resp.Status),
		}
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}

func (c *HealthChecker) checkSpannerComponent(ctx context.Context, name string, client health.SpannerHealthClient, expectedVersion int64) (*spb.ComponentHealth, string) {
	if client == nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: "spanner client not initialized",
		}, ""
	}

	versionVal, dirty, err := client.QuerySchemaVersion(ctx)
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("database check failed: %v", err),
		}, ""
	}

	versionStr := fmt.Sprintf("%d", versionVal)
	if dirty {
		versionStr += " (dirty)"
	}

	if dirty {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("schema migration is in a dirty state at version %d", versionVal),
		}, versionStr
	}

	if versionVal != expectedVersion {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("schema version %d does not match expected version %d", versionVal, expectedVersion),
		}, versionStr
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}, versionStr
}

func (c *HealthChecker) checkRedis(ctx context.Context) *spb.ComponentHealth {
	if c.redisClient == nil {
		return &spb.ComponentHealth{
			Name:         "Redis",
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: "redis client not initialized",
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.redisClient.Ping(pingCtx); err != nil {
		return &spb.ComponentHealth{
			Name:         "Redis",
			Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("redis ping failed: %v", err),
		}
	}
	return &spb.ComponentHealth{
		Name:        "Redis",
		Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}
