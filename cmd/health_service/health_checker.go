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

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type HealthChecker struct {
	cfg        *Config
	httpClient *http.Client
	xidConn    *grpc.ClientConn
}

func newHealthChecker(cfg *Config) (*HealthChecker, error) {
	if cfg.XidAddr == "" {
		return nil, fmt.Errorf("xid address is required")
	}
	conn, err := grpc.NewClient(cfg.XidAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client for xid at %s: %w", cfg.XidAddr, err)
	}
	return &HealthChecker{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		xidConn: conn,
	}, nil
}

func (c *HealthChecker) Close() error {
	if c.xidConn != nil {
		return c.xidConn.Close()
	}
	return nil
}

func (c *HealthChecker) RunChecks(ctx context.Context) []*spb.ComponentHealth {
	targets := c.getCheckTargets()
	components := make([]*spb.ComponentHealth, len(targets))
	var wg sync.WaitGroup

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, t checkTarget) {
			defer wg.Done()
			components[idx] = t.checkFn(ctx)
		}(i, target)
	}

	wg.Wait()
	return components
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
				// TODO(b/532846936): Implement actual health checking for Log Monitor Cloud Run service.
				// For now, we mock it as healthy since Cloud Run ingress is internal-only.
				return &spb.ComponentHealth{
					Name:        "Log Monitor",
					Type:        spb.ComponentType_COMPONENT_TYPE_CORE_SERVICE,
					HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
				}
			},
		},
		{
			name: "Spanner",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				// TODO(b/532846936): Implement actual Spanner database connectivity checks.
				return &spb.ComponentHealth{
					Name:        "Spanner",
					Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
					HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
				}
			},
		},
		{
			name: "Redis",
			checkFn: func(ctx context.Context) *spb.ComponentHealth {
				// TODO(b/532846936): Implement actual Redis cache connectivity check.
				return &spb.ComponentHealth{
					Name:        "Redis",
					Type:        spb.ComponentType_COMPONENT_TYPE_EXTERNAL_DEPENDENCY,
					HealthState: spb.HealthState_HEALTH_STATE_HEALTHY,
				}
			},
		},
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
