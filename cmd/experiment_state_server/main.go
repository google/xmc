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
	"xmanager_cloud/internal/storage"
	oauth "xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/auth/principal"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
	"xmanager_cloud/pkg/components/store/memory"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed static/*
var embedFS embed.FS

func startGRPCServer(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config, oauthAuth *oauth.Authenticator, prodServer *server.ProdServer) (*grpc.Server, func()) {
	transportCreds, err := tlsutil.NewServerCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure server credentials", "error", err)
	}
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "failed to listen", "error", err)
	}

	s := grpc.NewServer(grpc.Creds(transportCreds))
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(s, healthServer)
	healthServer.SetServingStatus("xmc.ExperimentStateService", healthpb.HealthCheckResponse_SERVING)

	spb.RegisterExperimentStateServiceServer(s, prodServer)
	cmsAuth, err := newCMSAuthorizer(ctx, cfg, oauthCfg, oauthAuth)
	if err != nil {
		logging.Fatal(ctx, "failed to initialize CMS authorizer", "error", err)
	}
	stopCMS := initCMS(ctx, s, healthServer, prodServer.Storage(), cmsAuth)
	reflection.Register(s)

	logging.Info(ctx, "gRPC server listening", "addr", lis.Addr())
	go func() {
		if err := s.Serve(lis); err != nil {
			logging.Fatal(ctx, "failed to serve", "error", err)
		}
	}()
	return s, stopCMS
}

func startHTTPGateway(ctx context.Context, cfg *config.Config, oauthCfg *oauth.Config, oauthAuth *oauth.Authenticator, prodServer *server.ProdServer) {
	httpTransportCreds, err := tlsutil.NewClientCredentials(ctx, cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		logging.Fatal(ctx, "failed to configure client credentials", "error", err)
	}

	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(httpTransportCreds)}
	if err := spb.RegisterExperimentStateServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts); err != nil {
		logging.Fatal(ctx, "failed to register gateway", "error", err)
	}

	spHealth, xidHealth, tqHealth := prodServer.HealthCheckers()
	httpMux := setupHTTPMux(ctx, oauthAuth, oauthAuth.Authenticate(gwmux), activeHealthCheck(spHealth, xidHealth, tqHealth), livenessCheck())
	registerCLILogin(ctx, httpMux, oauthAuth, oauthCfg, prodServer.Storage())

	gwServer := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: oauthAuth.SecurityMiddlewares(httpMux),
	}
	go func() {
		logging.Info(ctx, "Starting gRPC gateway", "addr", gwServer.Addr)
		if err := gwServer.ListenAndServe(); err != nil {
			logging.Fatal(ctx, "failed to serve gRPC gateway", "error", err)
		}
	}()
}

// main starts the gRPC server and handles graceful shutdown.
func main() {
	flag.Parse()

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

	oauthAuth := oauth.NewAuthenticator(oauthCfg)
	defer oauthAuth.Close()

	// The gRPC authorizer reuses this authenticator for admission, so gRPC admits the same principals as HTTP.
	prodServer, err := server.NewProd(ctx, &cfg, oauthCfg, authz.DeploymentAdmission(oauthAuth))
	if err != nil {
		logging.Fatal(ctx, "failed to initialize production server", "error", err)
	}
	defer prodServer.Close()

	s, stopCMS := startGRPCServer(ctx, &cfg, oauthCfg, oauthAuth, prodServer)
	// Deferred after prodServer.Close, so it runs after s.GracefulStop and before the
	// database handle is closed.
	defer stopCMS()

	startHTTPGateway(ctx, &cfg, oauthCfg, oauthAuth, prodServer)

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

type cmsAdmissionChecker struct {
	// invokerSA is in canonical form (see principal.Normalize).
	invokerSA string
	delegate  authz.AdmissionChecker
}

func (c cmsAdmissionChecker) IsUserAuthorized(ctx context.Context, email string) (bool, error) {
	if c.invokerSA != "" && principal.Normalize(email) == c.invokerSA {
		return true, nil
	}
	return c.delegate.IsUserAuthorized(ctx, email)
}

// cmsAdmission admits invokerSA in addition to every principal the deployment admits. invokerSA
// (ess_service_account, the chart's gcpServiceAccount) is the workload service account that the
// xmc-bridge-sa KSA is bound to through Workload Identity, so it is the identity xmc-bridge calls CMS
// with; it usually holds no AUTHORIZED_IAM_ROLE binding. It is checked first, so a CRM outage does
// not block the bridge.
func cmsAdmission(invokerSA string, oauthAuth *oauth.Authenticator) authz.AdmissionChecker {
	delegate := authz.DeploymentAdmission(oauthAuth)
	if delegate == nil {
		return nil
	}
	return cmsAdmissionChecker{
		invokerSA: principal.Normalize(invokerSA),
		delegate:  delegate,
	}
}

func buildCMSAuthorizer(cfg *config.Config, oauthCfg *oauth.Config, userAuth cms.UserAuthenticator, adminChecker cms.AdminChecker) cms.CallerAuthorizer {
	// oauthCfg comes from oauth.LoadConfig, which rejects DISABLE_AUTHENTICATION in binaries built
	// without the mockauth tag; DevAuthCompiledIn also makes this bypass dead code there.
	if oauth.DevAuthCompiledIn() && oauthCfg != nil && oauthCfg.DisableAuthentication {
		return cms.NewDisabledAuthorizer()
	}
	var checker cms.AdminChecker
	if oauthCfg == nil || (!oauthCfg.IsNonProdAuth() && oauthCfg.AdminIAMRole != "") || oauthCfg.MockAuth {
		checker = adminChecker
	}
	return cms.NewAuthorizer(cms.AuthorizerConfig{Authenticator: userAuth, AdminChecker: checker, AuthorizedServiceAccounts: []string{cfg.InvokerSA}})
}

// initCMS registers CMS on s and starts its ownership metrics collector. The returned stop
// function cancels the collector and waits for it to exit.
func initCMS(ctx context.Context, s *grpc.Server, healthServer *health.Server, st storage.Storage, auth cms.CallerAuthorizer) (stop func()) {
	logging.Info(ctx, "Initializing Component Manager Service (CMS)...")
	var cmsStore interface {
		store.Store
		store.OwnershipGaugeReader
	}
	if st != nil {
		dbStore, err := dbstore.New(st)
		if err != nil {
			logging.Fatal(ctx, "failed to create dbstore.Store for Component Manager Service", "error", err)
		}
		cmsStore = dbStore
		logging.Info(ctx, "Using dbstore.Store for Component Manager Service")
	} else {
		cmsStore = memory.NewMemoryStore()
		logging.Warn(ctx, "Storage not active; using in-memory MemoryStore fallback for Component Manager Service")
	}
	collectorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cms.RunOwnershipMetricsCollector(collectorCtx, cmsStore, cms.OwnershipMetricsInterval)
	}()
	cmsServer := cms.NewServer(cmsStore, cms.WithAuthorizer(auth))
	componentspb.RegisterComponentManagerServiceServer(s, cmsServer)
	healthServer.SetServingStatus("cloud.xmanager.components.v1alpha1.ComponentManagerService", healthpb.HealthCheckResponse_SERVING)
	logging.Info(ctx, "Component Manager Service (CMS) registered successfully")
	return func() {
		cancel()
		<-done
	}
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
