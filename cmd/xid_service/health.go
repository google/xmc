// Copyright 2025 Google LLC
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
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthgrpc "grpc_proto/health_go_grpc"
	healthpb "grpc_proto/health_go_proto"
	xmchealth "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/logging"
)

const (
	healthCheckInterval       = 5 * time.Second
	spannerTimeout            = 2 * time.Second
	xidServiceHealthCheckName = "xmc.XidService"
)

// RegisterHealthServer registers the health server on the gRPC server.
func RegisterHealthServer(s *grpc.Server) *health.Server {
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthgrpc.RegisterHealthServer(s, healthServer)
	return healthServer
}

// StartHealthMonitor periodically checks dependencies and updates the gRPC health server.
func StartHealthMonitor(ctx context.Context, sc *spanner.Client, healthServer *health.Server) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	// Initial check
	updateHealthStatus(ctx, sc, healthServer)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateHealthStatus(ctx, sc, healthServer)
		}
	}
}

func updateHealthStatus(ctx context.Context, sc *spanner.Client, healthServer *health.Server) {
	if err := checkSpanner(ctx, sc); err != nil {
		logging.Error(ctx, "Active health check failed", "error", err)
		healthServer.SetServingStatus(xidServiceHealthCheckName, healthpb.HealthCheckResponse_NOT_SERVING)
	} else {
		healthServer.SetServingStatus(xidServiceHealthCheckName, healthpb.HealthCheckResponse_SERVING)
	}
}

func checkSpanner(ctx context.Context, sc *spanner.Client) error {
	ctx, cancel := context.WithTimeout(ctx, spannerTimeout)
	defer cancel()

	return xmchealth.CheckSpanner(ctx, sc)
}
