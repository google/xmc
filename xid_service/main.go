// Copyright 2025 Google LLC
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
// Package main implements the XID Service for xmanager_cloud.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go/google"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/google_api/option/option"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	authz "xmanager_cloud/auth/auth"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/xid_service/config"
	"xmanager_cloud/xid_service/server/server"

	ss "xmanager_cloud/storage/spanner/spanner"

	spb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

func main() {
	google.Init()

	// 1. Load configuration
	cfg := config.Load()

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.QuotaProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	// 2. Initialize Spanner client.
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	sc, err := spanner.NewClientWithConfig(
		ctx,
		cfg.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.QuotaProjectID),
	)
	if err != nil {
		logging.Fatal(ctx, "failed to create spanner client", "error", err)
	}
	defer sc.Close()

	// 3. Initialize Cloud Identity service.
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(cfg.QuotaProjectID))
	if err != nil {
		logging.Fatal(ctx, "failed to create Cloud Identity service", "error", err)
	}

	// 4. Inject dependencies into the server
	auth, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{cfg.Audience, cfg.HTTPAudience, cfg.GRPCAudience}, cfg.IAPIssuer, cfg.DisableAuthentication)
	if err != nil {
		logging.Fatal(ctx, "failed to create authorizer", "error", err)
	}

	storage, err := ss.NewStorage(sc)
	if err != nil {
		logging.Fatal(ctx, "failed to create storage", "error", err)
	}

	xidServer := server.NewXIDServer(cfg, auth, storage)

	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		logging.Fatal(ctx, "failed to listen", "error", err)
	}

	s := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	spb.RegisterXidServiceServer(s, xidServer)

	reflection.Register(s)

	// Start active monitoring of dependencies
	go MonitorHealth(ctx, sc, s)

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
