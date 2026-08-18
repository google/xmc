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

// Package main implements the XID Service for xmanager_cloud.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"go/google"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	"xmanager_cloud/logging/logging"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/xid_service/config"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	"xmanager_cloud/xid_service/server/server"
)

func main() {
	google.Init()

	// 1. Load configuration
	cfg := config.Load()

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.QuotaProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	oauthCfg, err := oauth.LoadConfig()
	if err != nil {
		logging.Fatal(ctx, "failed to load oauth configuration", "error", err)
	}

	// 2. Initialize runtime server.
	runtimeServer, err := server.NewRuntimeServer(ctx, &cfg, oauthCfg)
	if err != nil {
		logging.Fatal(ctx, "failed to initialize runtime server", "error", err)
	}
	defer func() {
		if err := runtimeServer.Close(); err != nil {
			logging.Error(ctx, "failed to close runtime server", "error", err)
		}
	}()

	// 3. Set up the gRPC server.
	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		logging.Fatal(ctx, "failed to listen", "error", err)
	}

	s := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	apipb.RegisterXidServiceServer(s, runtimeServer)

	reflection.Register(s)

	// Start active monitoring of dependencies
	healthServer := RegisterHealthServer(s)
	go StartHealthMonitor(ctx, runtimeServer.HealthCheckers(), healthServer)

	logging.Info(ctx, "gRPC server listening", "addr", lis.Addr())

	go func() {
		if err := s.Serve(lis); err != nil {
			logging.Fatal(ctx, "failed to serve gRPC", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // Block until a signal is received

	logging.Info(ctx, "Shutting down gRPC server...")
	s.GracefulStop()
	logging.Info(ctx, "Server gracefully stopped.")
}
