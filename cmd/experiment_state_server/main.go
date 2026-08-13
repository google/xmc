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
// Command experiment_state_server implements the Experiment State Server for XMC.
//
// The Experiment State Server provides a gRPC API for managing ML experiments.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go/google"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/google_api/option/option"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/health/health"
	"golang/grpc/reflection/reflection"
	healthgrpc "grpc_proto/health_go_grpc"
	healthpb "grpc_proto/health_go_proto"

	authz "xmanager_cloud/auth/auth"
	"xmanager_cloud/cmd/experiment_state_server/toollibs"
	commonhealth "xmanager_cloud/common/health"
	dashboard_pb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/experiment_state_server/config"
	gwpb "xmanager_cloud/experiment_state_server/proto/api_go_grpc_gateway_proto"
	spb "xmanager_cloud/experiment_state_server/proto/api_go_grpc_proto"
	"xmanager_cloud/experiment_state_server/server/server"
	"xmanager_cloud/internal/security/tlsutil"
	"xmanager_cloud/logging/logging"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/security/keyreloader"
	ss "xmanager_cloud/storage/spanner/spanner"
	tq "xmanager_cloud/task_queue/client"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

//go:embed xmanager_cloud/experiment_state_server/static/*
var embedFS embed.FS

// main starts the gRPC server and handles graceful shutdown.
func main() {
	google.Init()

	// 1. Load configuration
	cfg := config.Load()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	ctx := logging.WithLogger(context.Background(), logger)

	oauthCfg, err := oauth.LoadConfig()
	if err != nil {
		logging.Fatal(ctx, "failed to load oauth configuration", "error", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	if cfg.InvokerSA == "" {
		logging.Fatal(ctx, "failed to load configuration, invoker service account is empty")
	}

	// 2. Initialize dependencies
	spannerClient := initSpannerClient(ctx, &cfg)
	defer spannerClient.Close()

	// 3. Initialize Cloud Identity, Storage, Auth, and XID services.
	storage, auth, xidClient := initServices(ctx, &cfg, oauthCfg, spannerClient)

	// 4. Initialize TLS credentials.
	transportCreds, err := tlsutil.NewServerCredentials(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure server credentials", "error", err)
	}

	opts := []grpc.ServerOption{
		grpc.Creds(transportCreds),
	}

	// 5. Initialize Task Queue client.
	taskQueueClient := initTaskQueueClient(ctx, &cfg)
	defer taskQueueClient.Close()

	// 5.5 Initialize Dashboard client.
	logging.Info(ctx, "Connecting to Dashboard service", "addr", cfg.DashboardServiceAddr)
	dashboardConn, err := grpc.NewClient(cfg.DashboardServiceAddr, []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}...)
	if err != nil {
		logging.Fatal(ctx, "failed to create gRPC client for Dashboard service", "error", err)
	}
	defer dashboardConn.Close()
	dashboardToolClient := toollibs.NewDashboardToolClient(dashboard_pb.NewDashboardServiceClient(dashboardConn))

	// 6. Set up the gRPC server.
	experimentStateServer := server.New(storage, &cfg, auth, xidClient, taskQueueClient, dashboardToolClient)

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "failed to listen", "error", err)
	}

	s := grpc.NewServer(opts...)

	spb.RegisterExperimentStateServiceServer(s, experimentStateServer)

	reflection.Register(s)

	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(s, healthServer)

	healthServer.SetServingStatus("xmc.ExperimentStateService", healthpb.HealthCheckResponse_SERVING)

	logging.Info(ctx, "gRPC server listening", "addr", lis.Addr())

	go func() {
		if err := s.Serve(lis); err != nil {
			logging.Fatal(ctx, "failed to serve", "error", err)
		}
	}()

	// 7. Initialize the gRPC gateway mux.
	httpTransportCreds, err := tlsutil.NewClientCredentials(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure client credentials", "error", err)
	}

	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(httpTransportCreds),
	}
	err = gwpb.RegisterExperimentStateServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		logging.Fatal(ctx, "failed to register gateway", "error", err)
	}

	// 8. Create a master HTTP mux for the gRPC gateway + health checks.
	httpMux := http.NewServeMux()

	// Load dynamic OAuth2 Authentication configurations
	oauthAuth := oauth.NewAuthenticator(oauthCfg)
	configureAuth(oauthAuth, oauthCfg)
	defer oauthAuth.Close()

	// Mount OAuth2 routing handlers
	httpMux.HandleFunc("/api/login", oauthAuth.HandleLogin)
	httpMux.HandleFunc("/api/callback", oauthAuth.HandleCallback)
	httpMux.HandleFunc("/api/logout", oauthAuth.HandleLogout)
	httpMux.Handle("/api/user", oauthAuth.Authenticate(http.HandlerFunc(oauthAuth.HandleUser)))

	staticRootFS, err := fs.Sub(embedFS, "xmanager_cloud/experiment_state_server/static")
	if err != nil {
		logging.Fatal(ctx, "Failed to create static file system", "error", err)
	}
	staticServer := http.FileServer(http.FS(staticRootFS))

	httpMux.HandleFunc("/v1/static/", func(w http.ResponseWriter, r *http.Request) {
		// Prevent directory listing
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}

		// Determine Content-Type based on extension
		ext := filepath.Ext(r.URL.Path)
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			// Fallback if type can't be determined
			mimeType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mimeType)

		// The http.FileServer and http.StripPrefix expect the original URL path
		// to match the prefix. Since we are in HandleFunc("/v1/static/"),
		// the r.URL.Path already includes "/v1/static/". We let StripPrefix handle it.
		http.StripPrefix("/v1/static/", staticServer).ServeHTTP(w, r)
	})

	httpMux.HandleFunc("/readyz", activeHealthCheck(commonhealth.NewSpannerHealthClient(spannerClient), xidClient, taskQueueClient))
	httpMux.HandleFunc("/healthz", livenessCheck())

	// Secure the private gRPC-Gateway endpoints with the Authenticate middleware.
	authenticatedGwmux := oauthAuth.Authenticate(gwmux)

	// Register a handler for the "/" path, aka. all traffic to the HTTP port.
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the path is exactly "/". This serves as a simple "live" check
		// for load balancers that default to "/" if not overridden.
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}

		// Delegate to the gRPC-Gateway, secured by OAuth2.
		authenticatedGwmux.ServeHTTP(w, r)
	})

	// 9. Apply security middlewares
	handler := oauthAuth.SecurityMiddlewares(httpMux)

	// 10. Start the HTTP server.
	gwServer := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: handler,
	}
	go func() {
		logging.Info(ctx, "Starting gRPC gateway", "addr", gwServer.Addr)
		if err := gwServer.ListenAndServe(); err != nil {
			logging.Fatal(ctx, "failed to serve gRPC gateway", "error", err)
		}
	}()

	// 11. Gracefully shut down the server when a signal is received.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // Block until a signal is received

	logging.Info(ctx, "Shutting down gRPC server")
	s.GracefulStop()
	logging.Info(ctx, "Waiting for background tasks to complete")
	experimentStateServer.Wait()
	logging.Info(ctx, "Server gracefully stopped")
}

func initSpannerClient(ctx context.Context, cfg *config.Config) *spanner.Client {
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	client, err := spanner.NewClientWithConfig(
		ctx,
		cfg.SpannerDatabaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(cfg.QuotaProjectID),
	)
	if err != nil {
		logging.Fatal(ctx, "failed to create spanner client", "error", err)
	}
	return client
}

func initServices(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config, spannerClient *spanner.Client) (*ss.Storage, *authz.Authorizer, xidgrpcpb.XidServiceClient) {
	idService, err := cloudidentity.NewService(ctx)
	if err != nil {
		logging.Fatal(ctx, "failed to create cloud identity service", "error", err)
	}

	storage, err := ss.NewStorageWithConfig(spannerClient, ss.StorageConfig{StatusMessageHistoryEnabled: cfg.StatusMessageHistoryEnabled})
	if err != nil {
		logging.Fatal(ctx, "failed to create storage", "error", err)
	}

	auth, err := authz.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{cfg.HTTPAudience, cfg.GRPCAudience, oauthCfg.ClientID}, cfg.IAPIssuer, false, []byte(oauthCfg.JWTSecret))
	if err != nil {
		logging.Fatal(ctx, "failed to create authorizer", "error", err)
	}

	logging.Info(ctx, "Connecting to XID service", "addr", cfg.XidServiceAddr)
	conn, err := grpc.NewClient(cfg.XidServiceAddr, []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}...)
	if err != nil {
		logging.Fatal(ctx, "failed to create gRPC client for XID service", "error", err)
	}
	return storage, auth, xidgrpcpb.NewXidServiceClient(conn)
}

func initTaskQueueClient(ctx context.Context, cfg *config.Config) *tq.Client {
	keyReloader, err := keyreloader.NewSignerReloader(cfg.SigningKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to load signing key", "error", err)
	}
	taskQueueClient, err := tq.New(ctx, cfg.ProjectID, cfg.InvokerSA, cfg.ActuatorURL, cfg.ActuatorAudience, keyReloader.Signer, cfg.SignatureTTLSeconds, cfg.RedisAddr)
	if err != nil {
		logging.Fatal(ctx, "failed to create task queue client", "error", err)
	}
	return taskQueueClient
}
