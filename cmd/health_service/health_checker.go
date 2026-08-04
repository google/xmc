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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

type HealthChecker struct {
	cfg        *Config
	httpClient *http.Client
}

func newHealthChecker(cfg *Config) *HealthChecker {
	return &HealthChecker{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (c *HealthChecker) RunChecks(ctx context.Context) []*spb.ComponentHealth {
	components := make([]*spb.ComponentHealth, 7)
	var wg sync.WaitGroup
	wg.Add(4)

	// 1. ESS
	go func() {
		defer wg.Done()
		components[0] = c.checkHTTPComponent(ctx, "ESS", c.cfg.EssURL, "/readyz")
	}()

	// 2. XID
	go func() {
		defer wg.Done()
		components[1] = c.checkGRPCComponent(ctx, "XID", c.cfg.XidAddr, "xmc.XidService")
	}()

	// 3. Actuator
	go func() {
		defer wg.Done()
		components[2] = c.checkHTTPComponent(ctx, "Actuator", c.cfg.ActuatorURL, "/readyz")
	}()

	// 4. Reconciler
	go func() {
		defer wg.Done()
		components[3] = c.checkHTTPComponent(ctx, "Reconciler", c.cfg.ReconcilerURL, "/readyz")
	}()

	// 5. Log Monitor
	// TODO(b/532846936): Implement actual health checking for Log Monitor Cloud Run service.
	// For now, we mock it as healthy since Cloud Run ingress is internal-only.
	components[4] = &spb.ComponentHealth{
		Name:        "Log Monitor",
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	// 6. Spanner
	// TODO(b/532846936): Implement actual Spanner database connectivity checks.
	components[5] = &spb.ComponentHealth{
		Name:        "Spanner",
		Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	// 7. Redis
	// TODO(b/532846936): Implement actual Redis cache connectivity check.
	components[6] = &spb.ComponentHealth{
		Name:        "Redis",
		Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}

	wg.Wait()
	return components
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
			ErrorMessage: fmt.Sprintf("HTTP request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("unexpected HTTP status: %s", resp.Status),
		}
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}

func (c *HealthChecker) checkGRPCComponent(ctx context.Context, name string, addr string, serviceName string) *spb.ComponentHealth {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("gRPC dial failed: %v", err),
		}
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: serviceName})
	if err != nil {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("gRPC Check call failed: %v", err),
		}
	}

	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return &spb.ComponentHealth{
			Name:         name,
			Type:         spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
			HealthState:  spb.HealthState_HEALTH_STATE_UNHEALTHY,
			ErrorMessage: fmt.Sprintf("gRPC service status: %s", resp.Status),
		}
	}

	return &spb.ComponentHealth{
		Name:        name,
		Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
		HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
	}
}
