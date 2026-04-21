package main

import (
	"context"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/grpc/grpc"
	"golang/grpc/health/health"
	healthgrpc "grpc_proto/health_go_grpc"
	healthpb "grpc_proto/health_go_proto"
	commonhealth "xmanager_cloud/common/health"
	"xmanager_cloud/logging/logging"
)

const (
	healthCheckInterval       = 5 * time.Second
	spannerTimeout            = 2 * time.Second
	xidServiceHealthCheckName = "xmc.XidService"
)

// MonitorHealth periodically checks dependencies and updates the gRPC health server.
func MonitorHealth(ctx context.Context, sc *spanner.Client, s *grpc.Server) {
	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(s, healthServer)

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

	return commonhealth.CheckSpanner(ctx, sc)
}
