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

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/security/tlsutil"
	"xmanager_cloud/internal/storage/spanner/migrations/version"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/task_queue_redis/redisclient"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	cloudidentity "google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/api/option"

	run "google.golang.org/api/run/v2"

	spb "xmanager_cloud/health_service/proto/api_go_proto"
)

type grpcServerDeps struct {
	grpcServer     *grpc.Server
	listener       net.Listener
	srv            *server
	lazySpanner    *HealthServiceLazySpannerWrapper
	redisClient    redisclient.Publisher
	pauseScheduler PauseScheduler
}

func (d *grpcServerDeps) Close(ctx context.Context) {
	if d.lazySpanner != nil {
		if err := d.lazySpanner.Close(); err != nil {
			logging.Error(ctx, "Failed to close lazy spanner wrapper", "error", err)
		}
	}
	if d.redisClient != nil {
		cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := d.redisClient.Close(cleanupCtx); err != nil {
			logging.Error(ctx, "Failed to close redis client", "error", err)
		}
	}
	if d.pauseScheduler != nil {
		if err := d.pauseScheduler.Close(); err != nil {
			logging.Error(ctx, "Failed to close pause scheduler", "error", err)
		}
	}
}

func main() {
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

	lazySpanner := NewHealthServiceLazySpannerWrapper(cfg.EssSpannerDatabase, cfg.XidSpannerDatabase, nil)

	var redisPingClient RedisPingClient
	var publisher redisclient.Publisher
	if cfg.RedisAddr != "" {
		redisCfg := redisclient.Config{Addr: cfg.RedisAddr}
		publisher = redisclient.NewPublisher(redisCfg, constants.RedisResumeStream)
		redisPingClient = publisher
	}

	var cloudRunService *run.Service
	if cfg.LogMonitorURL == "" && cfg.LogMonitorResourceName != "" {
		cloudRunService, err = run.NewService(ctx)
		if err != nil {
			logging.Warn(ctx, "Failed to create cloud run client, Cloud Run checks will report UNSPECIFIED", "error", err)
		}
	}

	checker, err := NewHealthChecker(HealthCheckerParams{
		Config:                   cfg,
		LazySpanner:              lazySpanner,
		RedisClient:              redisPingClient,
		CloudRunService:          cloudRunService,
		EssExpectedSchemaVersion: version.EssExpectedSchemaVersion(),
		XidExpectedSchemaVersion: version.XidExpectedSchemaVersion(),
	})
	if err != nil {
		logging.Fatal(ctx, "Failed to create health checker", "error", err)
	}

	authorizer := initAuthorizer(ctx, oauthCfg)
	guard := NewAdminRoleGuard(AdminRoleGuardParams{
		Authorizer:    authorizer,
		Authenticator: authenticator,
	})
	pauseScheduler := initPauseScheduler(cfg, lazySpanner, publisher)
	srv := newServer(cfg, checker, guard, lazySpanner, pauseScheduler)
	spb.RegisterHealthServiceServer(grpcServer, srv)
	reflection.Register(grpcServer)

	return &grpcServerDeps{
		grpcServer:     grpcServer,
		listener:       lis,
		srv:            srv,
		lazySpanner:    lazySpanner,
		redisClient:    publisher,
		pauseScheduler: pauseScheduler,
	}
}

// initPauseScheduler wires the pause scheduler. Redis clients need no lazy wrapper: go-redis does no
// network I/O at construction time, dials on the first command, and reconnects after failures.
func initPauseScheduler(cfg *Config, lazyStorage LazyStorageProvider, taskQueuePublisher redisclient.Publisher) PauseScheduler {
	var broadcastPublisher broadcast.Publisher
	if cfg.RedisAddr != "" {
		broadcastRedisClient := redisclient.NewClient(redisclient.Config{
			Addr: cfg.RedisAddr,
		})
		broadcastPublisher = broadcast.NewPublisher(broadcastRedisClient, constants.RedisPauseStream)
	}

	return NewPauseScheduler(PauseSchedulerParams{
		LazyStorage:         lazyStorage,
		BroadcastPublisher:  broadcastPublisher,
		TaskQueuePublisher:  taskQueuePublisher,
		BatchSize:           cfg.SchedulingResumeBatchSize,
		MaxBacklogThreshold: cfg.SchedulingResumeMaxBacklog,
	})
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
	err = spb.RegisterHealthServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
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
	mux.Handle("/metrics", promhttp.Handler())

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
