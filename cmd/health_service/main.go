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
	"io"
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
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	health "xmanager_cloud/common/health"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/security/tlsutil"
	"xmanager_cloud/internal/storage/spanner/migrations/version"
	"xmanager_cloud/internal/storage/spanner/pausestate"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/summary"
	"xmanager_cloud/task_queue_redis/redisclient"

	cloudidentity "google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/api/option"

	run "google.golang.org/api/run/v2"

	gwpb "xmanager_cloud/health_service/proto/api_go_grpc_gateway_proto"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

type grpcServerDeps struct {
	grpcServer        *grpc.Server
	listener          net.Listener
	srv               *server
	essSpannerClient  health.SpannerHealthClient
	xidSpannerClient  health.SpannerHealthClient
	redisClient       *redisclient.PublisherClient
	compSpannerClient *spanner.Client
}

func (d *grpcServerDeps) Close(ctx context.Context) {
	if closer, ok := d.essSpannerClient.(io.Closer); ok {
		_ = closer.Close()
	}
	if closer, ok := d.xidSpannerClient.(io.Closer); ok {
		_ = closer.Close()
	}
	if d.redisClient != nil {
		cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := d.redisClient.Close(cleanupCtx); err != nil {
			logging.Error(ctx, "Failed to close redis client", "error", err)
		}
	}
	if d.compSpannerClient != nil {
		d.compSpannerClient.Close()
	}
}

func main() {
	google.Init()
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		logging.Fatal(ctx, "Failed to load OAuth config", "error", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)

	deps := initGRPCServer(ctx, cfg, *oauthCfg, authenticator)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		deps.Close(cleanupCtx)
	}()

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

func initSpannerHealthClient(ctx context.Context, dbPath string, name string) health.SpannerHealthClient {
	if dbPath == "" {
		return nil
	}
	lazyClient := NewLazySpannerHealthClient(dbPath, nil)
	if err := lazyClient.Initialize(ctx); err != nil {
		logging.Warn(ctx, fmt.Sprintf("Failed to eagerly initialize %s Spanner client; will retry lazily on request", name), "database", dbPath, "error", err)
	}
	return lazyClient
}

func initCustomComponentsProvider(ctx context.Context, cfg *Config) (summary.CustomComponentsInfoProvider, *spanner.Client) {
	if !cfg.EnableComponents {
		return nil, nil
	}

	if cfg.EssSpannerDatabase == "" {
		logging.Warn(ctx, "EssSpannerDatabase not set; custom components health checks disabled")
		return nil, nil
	}

	compSpannerClient, err := spanner.NewClient(ctx, cfg.EssSpannerDatabase)
	if err != nil {
		logging.Warn(ctx, "Failed to create Spanner client for components store; custom components checks disabled", "error", err)
		return nil, nil
	}

	compStore := store.NewSpannerStore(compSpannerClient)
	logging.Info(ctx, "Using SpannerStore for custom components discovery")

	storeProvider, err := summary.NewStoreComponentsProvider(compStore)
	if err != nil {
		compSpannerClient.Close()
		logging.Warn(ctx, "Failed to create custom components info provider; custom components checks disabled", "error", err)
		return nil, nil
	}
	return storeProvider, compSpannerClient
}

func initGRPCServer(ctx context.Context, cfg *Config, oauthCfg oauth2.Config, authenticator *oauth2.Authenticator) *grpcServerDeps {
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "Failed to listen on gRPC port", "error", err)
	}

	transportCreds, err := tlsutil.NewServerCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "Failed to create server credentials", "error", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(transportCreds))

	essSpannerClient := initSpannerHealthClient(ctx, cfg.EssSpannerDatabase, "ESS")
	xidSpannerClient := initSpannerHealthClient(ctx, cfg.XidSpannerDatabase, "XID")

	var redisPingClient RedisPingClient
	var publisher *redisclient.PublisherClient
	if cfg.RedisAddr != "" {
		redisCfg := redisclient.Config{Addr: cfg.RedisAddr}
		publisher = redisclient.NewPublisher(redisCfg, "")
		redisPingClient = publisher
	}

	cloudRunService, err := run.NewService(ctx)
	if err != nil {
		logging.Warn(ctx, "Failed to create cloud run client, Cloud Run checks will report UNSPECIFIED", "error", err)
	}

	customProvider, compSpannerClient := initCustomComponentsProvider(ctx, cfg)

	checker, err := NewHealthChecker(HealthCheckerParams{
		Config:                   cfg,
		EssHealthClient:          essSpannerClient,
		XidHealthClient:          xidSpannerClient,
		RedisClient:              redisPingClient,
		CloudRunService:          cloudRunService,
		EssExpectedSchemaVersion: version.EssExpectedSchemaVersion(),
		XidExpectedSchemaVersion: version.XidExpectedSchemaVersion(),
		ComponentsProvider:       customProvider,
	})
	if err != nil {
		logging.Fatal(ctx, "Failed to create health checker", "error", err)
	}

	authorizer := initAuthorizer(ctx, oauthCfg)
	guard := NewAdminRoleGuard(AdminRoleGuardParams{
		Authorizer:    authorizer,
		Authenticator: authenticator,
	})
	pauseScheduler := initPauseScheduler(ctx, cfg)
	srv := newServer(cfg, checker, guard, customProvider, pauseScheduler)
	spb.RegisterHealthServiceServer(grpcServer, srv)
	reflection.Register(grpcServer)

	return &grpcServerDeps{
		grpcServer:        grpcServer,
		listener:          lis,
		srv:               srv,
		essSpannerClient:  essSpannerClient,
		xidSpannerClient:  xidSpannerClient,
		redisClient:       publisher,
		compSpannerClient: compSpannerClient,
	}
}

func initPauseScheduler(ctx context.Context, cfg *Config) PauseScheduler {
	logging.Warn(ctx, "Using in-memory fake for pause scheduling state repository")
	return NewPauseScheduler(pausestate.NewFake())
}

func initGatewayMux(ctx context.Context, cfg *Config, authenticator *oauth2.Authenticator) http.Handler {
	httpTransportCreds, err := tlsutil.NewClientCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
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

func initAuthorizer(ctx context.Context, oauthCfg oauth2.Config) auth.AuthorizerInterface {
	idService, err := cloudidentity.NewService(ctx, option.WithScopes(cloudidentity.CloudIdentityGroupsReadonlyScope))
	if err != nil {
		logging.Fatal(ctx, "Failed to create Cloud Identity service", "error", err)
	}

	var audiences []string
	if oauthCfg.ClientID != "" {
		audiences = append(audiences, oauthCfg.ClientID)
	}

	authorizer, err := auth.NewAuthorizer(
		ctx,
		idService,
		10*time.Minute,
		1*time.Minute,
		audiences,
		auth.DefaultIAPIssuer,
		[]byte(oauthCfg.JWTSecret),
	)
	if err != nil {
		logging.Fatal(ctx, "Failed to create authorizer", "error", err)
	}

	return authorizer
}
