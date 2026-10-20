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
	"flag"
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

	"cloud.google.com/go/spanner"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/cmd/experiment_state_server/server"
	authz "xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/security/tlsutil"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed static/*
var embedFS embed.FS

// main starts the gRPC server and handles graceful shutdown.
func main() {
	flag.Parse()

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

	// 2. Initialize the OAuth authenticator and production server. The gRPC authorizer reuses this
	// authenticator for admission, so gRPC admits the same principals as HTTP.
	oauthAuth := oauth.NewAuthenticator(oauthCfg)
	defer oauthAuth.Close()

	prodServer, err := server.NewProd(ctx, &cfg, oauthCfg, authz.DeploymentAdmission(oauthAuth))
	if err != nil {
		logging.Fatal(ctx, "failed to initialize production server", "error", err)
	}
	defer prodServer.Close()
	enableCLISessions(ctx, oauthAuth, prodServer.SpannerClient())

	// 3. Initialize TLS credentials.
	transportCreds, err := tlsutil.NewServerCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure server credentials", "error", err)
	}

	opts := []grpc.ServerOption{
		grpc.Creds(transportCreds),
	}

	// 4. Set up the gRPC server.
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "failed to listen", "error", err)
	}

	s := grpc.NewServer(opts...)

	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(s, healthServer)
	healthServer.SetServingStatus("xmc.ExperimentStateService", healthpb.HealthCheckResponse_SERVING)

	spb.RegisterExperimentStateServiceServer(s, prodServer)
	if cfg.EnableComponents {
		cmsAuth, err := newCMSAuthorizer(ctx, &cfg, oauthCfg, oauthAuth)
		if err != nil {
			logging.Fatal(ctx, "failed to initialize CMS authorizer", "error", err)
		}
		initCMS(ctx, s, healthServer, prodServer.SpannerClient(), cmsAuth)
	}

	reflection.Register(s)

	logging.Info(ctx, "gRPC server listening", "addr", lis.Addr())

	go func() {
		if err := s.Serve(lis); err != nil {
			logging.Fatal(ctx, "failed to serve", "error", err)
		}
	}()

	// 5. Initialize the gRPC gateway mux.
	httpTransportCreds, err := tlsutil.NewClientCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure client credentials", "error", err)
	}

	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(httpTransportCreds),
	}
	err = spb.RegisterExperimentStateServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		logging.Fatal(ctx, "failed to register gateway", "error", err)
	}

	// 6. Create a master HTTP mux for the gRPC gateway + health checks.
	spHealth, xidHealth, tqHealth := prodServer.HealthCheckers()
	httpMux := setupHTTPMux(ctx, oauthAuth, oauthAuth.Authenticate(gwmux), activeHealthCheck(spHealth, xidHealth, tqHealth), livenessCheck())
	registerCLILogin(ctx, httpMux, oauthAuth, oauthCfg, prodServer.SpannerClient())

	// 7. Apply security middlewares
	handler := oauthAuth.SecurityMiddlewares(httpMux)

	// 8. Start the HTTP server.
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

	// 9. Gracefully shut down the server when a signal is received.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // Block until a signal is received

	logging.Info(ctx, "Shutting down gRPC server")
	s.GracefulStop()
	logging.Info(ctx, "Server gracefully stopped")
}

// newCMSAuthorizer builds the Component Manager Service authorizer on a dedicated gRPC authorizer
// whose admission is cmsAdmission, so the xmc-bridge exemption never reaches other services.
func newCMSAuthorizer(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config, oauthAuth *oauth.Authenticator) (cms.CallerAuthorizer, error) {
	userAuth, err := server.NewGRPCAuthorizer(ctx, cfg, oauthCfg, cmsAdmission(cfg.InvokerSA, oauthAuth))
	if err != nil {
		return nil, err
	}
	return buildCMSAuthorizer(cfg, oauthCfg, userAuth, oauthAuth), nil
}

// cmsAdmission admits invokerSA in addition to every principal the deployment admits. invokerSA
// (ess_service_account, the chart's gcpServiceAccount) is the workload service account that the
// xmc-bridge-sa KSA is bound to through Workload Identity, so it is the identity xmc-bridge calls CMS
// with; it usually holds no AUTHORIZED_IAM_ROLE binding. It is checked first, so a CRM outage does
// not block the bridge.
func cmsAdmission(invokerSA string, oauthAuth *oauth.Authenticator) authz.AdmissionChecker {
	return authz.AnyOf(authz.AdmitPrincipals(invokerSA), authz.DeploymentAdmission(oauthAuth))
}

func buildCMSAuthorizer(cfg *config.Config, oauthCfg *oauth.Config, userAuth cms.UserAuthenticator, adminChecker cms.AdminChecker) cms.CallerAuthorizer {
	if (oauthCfg != nil && oauthCfg.DisableAuthentication) || os.Getenv("DISABLE_AUTHENTICATION") == "true" {
		return cms.NewDisabledAuthorizer()
	}
	var checker cms.AdminChecker
	if oauthCfg == nil || (!oauthCfg.IsNonProdAuth() && oauthCfg.AdminIAMRole != "") || oauthCfg.MockAuth {
		checker = adminChecker
	}
	return cms.NewAuthorizer(cms.AuthorizerConfig{Authenticator: userAuth, AdminChecker: checker, AuthorizedServiceAccounts: []string{cfg.InvokerSA}})
}

func initCMS(ctx context.Context, s *grpc.Server, healthServer *health.Server, spannerClient *spanner.Client, auth cms.CallerAuthorizer) {
	logging.Info(ctx, "Initializing Component Manager Service (CMS)...")
	var cmsStore store.Store
	if spannerClient != nil {
		cmsStore = store.NewSpannerStore(spannerClient)
		logging.Info(ctx, "Using SpannerStore for Component Manager Service")
	} else {
		cmsStore = store.NewMemoryStore()
		logging.Warn(ctx, "Spanner client not initialized; using in-memory MemoryStore fallback for Component Manager Service")
	}
	cmsServer := cms.NewServer(cmsStore, cms.WithAuthorizer(auth))
	componentspb.RegisterComponentManagerServiceServer(s, cmsServer)
	healthServer.SetServingStatus("cloud.xmanager.components.v1alpha1.ComponentManagerService", healthpb.HealthCheckResponse_SERVING)
	logging.Info(ctx, "Component Manager Service (CMS) registered successfully")
}

func setupHTTPMux(ctx context.Context, oauthAuth *oauth.Authenticator, authenticatedGwmux http.Handler, readyHandler http.Handler, livenessHandler http.Handler) *http.ServeMux {
	httpMux := http.NewServeMux()

	httpMux.HandleFunc("/api/login", oauthAuth.HandleLogin)
	httpMux.HandleFunc("/api/callback", oauthAuth.HandleCallback)
	httpMux.HandleFunc("/api/logout", oauthAuth.HandleLogout)
	httpMux.Handle("/api/user", oauthAuth.Authenticate(http.HandlerFunc(oauthAuth.HandleUser)))

	mountStaticFiles(ctx, httpMux)

	httpMux.Handle("/readyz", readyHandler)
	httpMux.Handle("/healthz", livenessHandler)
	httpMux.Handle("/metrics", promhttp.Handler())

	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}
		authenticatedGwmux.ServeHTTP(w, r)
	})

	return httpMux
}

func mountStaticFiles(ctx context.Context, httpMux *http.ServeMux) {
	staticRootFS, err := fs.Sub(embedFS, "static")
	if err != nil {
		logging.Fatal(ctx, "failed to create static file system", "error", err)
	}
	staticServer := http.FileServer(http.FS(staticRootFS))

	httpMux.HandleFunc("/v1/static/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		ext := filepath.Ext(r.URL.Path)
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mimeType)
		http.StripPrefix("/v1/static/", staticServer).ServeHTTP(w, r)
	})
}
