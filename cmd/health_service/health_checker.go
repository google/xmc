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

	spb "xmanager_cloud/api/protos/health"
	health "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/dbconfig"
	"xmanager_cloud/pkg/components/summary"

	run "google.golang.org/api/run/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type RedisPingClient interface {
	Ping(ctx context.Context) error
}

// LazyDBCheckerProvider provides lazy access to database schema version health clients and custom components provider.
type LazyDBCheckerProvider interface {
	LazyComponentsProvider
	EssHealth(ctx context.Context) (health.SchemaVersionClient, int64, error)
	XidHealth(ctx context.Context) (health.SchemaVersionClient, int64, error)
}

type HealthCheckerParams struct {
	Config          *Config
	LazyDB          LazyDBCheckerProvider
	RedisClient     RedisPingClient
	CloudRunService *run.Service
}

type HealthChecker struct {
	cfg             *Config
	httpClient      *http.Client
	xidConn         *grpc.ClientConn
	lazyDB          LazyDBCheckerProvider
	redisClient     RedisPingClient
	cloudRunService *run.Service
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
		xidConn:         conn,
		lazyDB:          params.LazyDB,
		redisClient:     params.RedisClient,
		cloudRunService: params.CloudRunService,
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

	// 2. Run database checks in parallel (ESS and XID only)
	var essComp, xidComp *spb.ComponentHealth
	var essVersion, xidVersion string

	wg.Add(1)
	go func() {
		defer wg.Done()
		essComp, essVersion = c.CheckESSDatabase(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		xidComp, xidVersion = c.CheckXIDDatabase(ctx)
	}()

	// 3. Run custom component checks in parallel if enabled
	var customCompHealths []summary.ComponentHealth
	var customCompErr error

	checkComponents := c.cfg != nil && c.cfg.EnableComponents && c.lazyDB != nil
	if checkComponents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			compCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			customCompHealths, customCompErr = c.checkCustomComponents(compCtx)
		}()
	}

	wg.Wait()

	// 4. Append database results to the components list
	components = append(components, essComp, xidComp)

	// 5. Append custom components health if enabled
	if checkComponents {
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

func (c *HealthChecker) checkCustomComponents(ctx context.Context) ([]summary.ComponentHealth, error) {
	provider, err := c.lazyDB.ComponentsProvider(ctx)
	if err != nil {
		return nil, err
	}
	return provider.GetComponentsHealth(ctx)
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
		Stability:    ch.Stability,
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
				if c.cfg.LogMonitorURL != "" {
					return c.checkHTTPComponent(ctx, "Log Monitor", c.cfg.LogMonitorURL, "/readyz")
				}
				return c.checkCloudRunComponent(ctx, "Log Monitor", c.cfg.LogMonitorResourceName)
			},
		},
		{
			name:    "Redis",
			checkFn: c.checkRedis,
		},
	}
}

func (c *HealthChecker) essDBComponentName() string {
	if c.cfg == nil {
		return "unknown"
	}
	switch c.cfg.DB.Backend {
	case dbconfig.BackendSpanner:
		return "Spanner ESS"
	case dbconfig.BackendPostgres:
		return "Postgres ESS"
	default:
		return "unknown"
	}
}

func (c *HealthChecker) xidDBComponentName() string {
	if c.cfg == nil {
		return "unknown"
	}
	switch c.cfg.DB.Backend {
	case dbconfig.BackendSpanner:
		return "Spanner XID"
	case dbconfig.BackendPostgres:
		return "Postgres XID"
	default:
		return "unknown"
	}
}

func (c *HealthChecker) CheckESSDatabase(ctx context.Context) (*spb.ComponentHealth, string) {
	name := c.essDBComponentName()
	if c.lazyDB == nil {
		return databaseNotInitializedHealth(name), ""
	}
	client, expectedVersion, err := c.lazyDB.EssHealth(ctx)
	if err != nil {
		return databaseErrorHealth(name, err), ""
	}
	return c.checkDatabaseComponent(ctx, name, client, expectedVersion)
}

func (c *HealthChecker) CheckXIDDatabase(ctx context.Context) (*spb.ComponentHealth, string) {
	name := c.xidDBComponentName()
	if c.lazyDB == nil {
		return databaseNotInitializedHealth(name), ""
	}
	client, expectedVersion, err := c.lazyDB.XidHealth(ctx)
	if err != nil {
		return databaseErrorHealth(name, err), ""
	}
	return c.checkDatabaseComponent(ctx, name, client, expectedVersion)
}

func databaseNotInitializedHealth(name string) *spb.ComponentHealth {
	return &spb.ComponentHealth{
		Name:         name,
		Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
		ErrorMessage: "database client not initialized",
	}
}

func databaseErrorHealth(name string, err error) *spb.ComponentHealth {
	return &spb.ComponentHealth{
		Name:         name,
		Type:         spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
		ErrorMessage: fmt.Sprintf("database check failed: %v", err),
	}
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

func (c *HealthChecker) checkDatabaseComponent(ctx context.Context, name string, client health.SchemaVersionClient, expectedVersion int64) (*spb.ComponentHealth, string) {
	if client == nil {
		return databaseNotInitializedHealth(name), ""
	}

	versionVal, dirty, err := client.QuerySchemaVersion(ctx)
	if err != nil {
		return databaseErrorHealth(name, err), ""
	}

	versionStr := fmt.Sprintf("%d", versionVal)
	if dirty {
		versionStr += " (dirty)"
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

func (c *HealthChecker) checkCloudRunComponent(ctx context.Context, name string, serviceName string) *spb.ComponentHealth {
	if serviceName == "" {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNSPECIFIED,
			ErrorMessage: "log monitor resource name is not configured",
		}
	}

	if c.cloudRunService == nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNSPECIFIED,
			ErrorMessage: "cloud run client not initialized",
		}
	}

	apiCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	resp, err := c.cloudRunService.Projects.Locations.Services.Get(serviceName).Context(apiCtx).Do()
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("failed to get service: %v", err),
		}
	}

	if resp == nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: "received empty response from cloud run API",
		}
	}

	if resp.TerminalCondition == nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: "terminal condition is missing",
		}
	}

	if resp.TerminalCondition.State != "CONDITION_SUCCEEDED" {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("service not ready: %s (reason: %s, message: %s)", resp.TerminalCondition.State, resp.TerminalCondition.Reason, resp.TerminalCondition.Message),
		}
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}
