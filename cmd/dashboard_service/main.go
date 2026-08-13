// Copyright 2026 Google LLC
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
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/spanner"
	"go/google"
	"golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/health/health"
	"golang/grpc/reflection/reflection"
	"google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/api/option"
	healthgrpc "grpc_proto/health_go_grpc"
	healthpb "grpc_proto/health_go_proto"

	"xmanager_cloud/cmd/dashboard_service/server"
	commonhealth "xmanager_cloud/common/health"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/internal/storage/spanner/dashboard"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/clickhouse"
)

func setupHTTPHandler(authenticator *oauth2.Authenticator, gwmux http.Handler, readinessCheck func(context.Context) error) http.Handler {
	mux := http.NewServeMux()

	// Probes
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if readinessCheck != nil {
			if err := readinessCheck(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(fmt.Sprintf("Service unavailable: %v", err)))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Secure private gRPC-Gateway endpoints with Authenticate middleware
	authenticatedGwmux := authenticator.Authenticate(gwmux)

	// Route gateway endpoints and LB probe fallback
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Dashboard service OK"))
			return
		}
		authenticatedGwmux.ServeHTTP(w, r)
	})

	return authenticator.SecurityMiddlewares(mux)
}

func main() {
	google.Init()
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	ctx := logging.WithLogger(context.Background(), logger)

	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		logging.Fatal(ctx, "Failed to load OAuth config", "error", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)

	clickhouseConn, err := clickhouse.Connect(ctx)
	var de *dataengine.DataEngine
	if err != nil {
		logging.Info(ctx, "ClickHouse connection not available at startup, initializing without data engine", "error", err)
	} else {
		de, err = dataengine.New(clickhouseConn)
		if err != nil {
			logging.Info(ctx, "Failed to initialize DataEngine", "error", err)
		}
	}

	if spannerDatabaseName == "" {
		logging.Fatal(ctx, "spanner_database_name is required but not configured")
	}
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	sc, err := spanner.NewClientWithConfig(
		ctx,
		spannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.ProjectID),
	)
	if err != nil {
		logging.Fatal(ctx, "failed to create spanner client", "error", err)
	}
	defer sc.Close()

	spannerRepo, err := dashboard.NewRepository(sc)
	if err != nil {
		logging.Fatal(ctx, "failed to initialize dashboard repository", "error", err)
	}
	var repo server.DashboardRepository = spannerRepo

	var authorizer auth.AuthorizerInterface
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(cfg.ProjectID))
	if err != nil {
		logging.Info(ctx, "Cloud Identity service not available at startup", "error", err)
	}
	authorizer, err = auth.NewAuthorizer(
		ctx,
		idService,
		10*time.Minute,
		1*time.Minute,
		[]string{oauthCfg.ClientID},
		auth.DefaultIAPIssuer,
		oauthCfg.MockAuth,
		[]byte(oauthCfg.JWTSecret),
	)
	if err != nil {
		logging.Info(ctx, "Authorizer initialized without full IAP configuration", "error", err)
	}

	// 1. Setup gRPC server
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "Failed to listen on gRPC port", "error", err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterDashboardServiceServer(grpcServer, server.NewServer(cfg, repo, authorizer, de))
	reflection.Register(grpcServer)

	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("xmc.DashboardService", healthpb.HealthCheckResponse_SERVING)

	errChan := make(chan error, 2)

	go func() {
		logging.Info(ctx, "Dashboard service gRPC server starting", "addr", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			errChan <- fmt.Errorf("gRPC server failed: %w", err)
		}
	}()

	// 2. Setup gRPC Gateway with header forwarding for auth
	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err = spb.RegisterDashboardServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		logging.Fatal(ctx, "Failed to register gateway", "error", err)
	}

	// 3. Setup HTTP Server with active readiness probe
	var readinessCheck func(context.Context) error
	if sc != nil {
		readinessCheck = func(ctx context.Context) error {
			checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return commonhealth.CheckSpanner(checkCtx, sc)
		}
	}

	handler := setupHTTPHandler(authenticator, gwmux, readinessCheck)

	addr := fmt.Sprintf(":%s", cfg.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	go func() {
		logging.Info(ctx, "Dashboard service HTTP gateway starting", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server failed: %w", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		logging.Info(ctx, "Shutting down dashboard service...")
	case err := <-errChan:
		logging.Info(ctx, "Server encountered error, shutting down...", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logging.Info(ctx, "Error shutting down HTTP server", "error", err)
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logging.Info(ctx, "gRPC server stopped gracefully")
	case <-time.After(5 * time.Second):
		logging.Warn(ctx, "gRPC graceful shutdown timed out, force stopping...")
		grpcServer.Stop()
	}

	logging.Info(ctx, "Dashboard service stopped")
}
