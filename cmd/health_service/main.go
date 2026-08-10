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

	"go/google"
	"golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	health "xmanager_cloud/common/health"
	"xmanager_cloud/internal/security/tlsutil"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/task_queue_redis/redisclient"

	"cloud.google.com/go/spanner"

	gwpb "xmanager_cloud/health_service/proto/api_go_grpc_gateway_proto"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

type grpcServerDeps struct {
	grpcServer *grpc.Server
	listener   net.Listener
	srv        *server
	essClient  *spanner.Client
	xidClient  *spanner.Client
}

func (d *grpcServerDeps) Close() {
	if d.essClient != nil {
		d.essClient.Close()
	}
	if d.xidClient != nil {
		d.xidClient.Close()
	}
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

	deps := initGRPCServer(ctx, cfg)
	defer deps.Close()

	go func() {
		logging.Info(ctx, "Health service gRPC starting", "addr", deps.listener.Addr())
		if err := deps.grpcServer.Serve(deps.listener); err != nil {
			logging.Fatal(ctx, "gRPC server failed", "error", err)
		}
	}()

	handler := initGatewayMux(ctx, cfg, authenticator)
	addr := fmt.Sprintf(":%s", cfg.Port)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	go func() {
		logging.Info(ctx, "Health service HTTP gateway starting", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Fatal(ctx, "HTTP server failed", "error", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logging.Info(ctx, "Shutting down health service...")
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logging.Info(ctx, "Error shutting down HTTP server", "error", err)
	}
	deps.grpcServer.GracefulStop()

	if err := deps.srv.Close(); err != nil {
		logging.Info(ctx, "Error closing server resources", "error", err)
	}

	logging.Info(ctx, "Health service stopped")
}

func initSpannerHealthClient(ctx context.Context, dbPath string, name string) (*spanner.Client, health.SpannerHealthClient) {
	if dbPath == "" {
		return nil, nil
	}
	client, err := spanner.NewClient(ctx, dbPath)
	if err != nil {
		logging.Warn(ctx, fmt.Sprintf("Failed to initialize %s Spanner client", name), "database", dbPath, "error", err)
		return nil, nil
	}
	return client, health.NewSpannerHealthClient(client)
}

func initGRPCServer(ctx context.Context, cfg *Config) *grpcServerDeps {
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "Failed to listen on gRPC port", "error", err)
	}

	transportCreds, err := tlsutil.NewServerCredentials(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "Failed to create server credentials", "error", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(transportCreds))

	essClient, essHealth := initSpannerHealthClient(ctx, cfg.EssSpannerDatabase, "ESS")
	xidClient, xidHealth := initSpannerHealthClient(ctx, cfg.XidSpannerDatabase, "XID")

	var redisPingClient RedisPingClient
	if cfg.RedisAddr != "" {
		redisCfg := redisclient.Config{Addr: cfg.RedisAddr}
		publisher := redisclient.NewPublisher(redisCfg, "")
		redisPingClient = publisher
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := publisher.Close(cleanupCtx); err != nil {
				logging.Error(ctx, "Failed to close redis client", "error", err)
			}
		}()
	}

	srv, err := newServer(cfg, essHealth, xidHealth, redisPingClient)
	if err != nil {
		logging.Fatal(ctx, "Failed to create server", "error", err)
	}
	spb.RegisterHealthServiceServer(grpcServer, srv)
	reflection.Register(grpcServer)

	return &grpcServerDeps{
		grpcServer: grpcServer,
		listener:   lis,
		srv:        srv,
		essClient:  essClient,
		xidClient:  xidClient,
	}
}

func initGatewayMux(ctx context.Context, cfg *Config, authenticator *oauth2.Authenticator) http.Handler {
	httpTransportCreds, err := tlsutil.NewClientCredentials(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "Failed to create client credentials", "error", err)
	}

	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(httpTransportCreds),
	}
	err = gwpb.RegisterHealthServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		logging.Fatal(ctx, "Failed to register gateway", "error", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	authenticatedGwmux := authenticator.Authenticate(gwmux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}
		authenticatedGwmux.ServeHTTP(w, r)
	})

	return authenticator.SecurityMiddlewares(mux)
}
