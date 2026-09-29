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
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	"xmanager_cloud/pkg/clickhouse/componentconfig"
	"xmanager_cloud/pkg/components/crreader"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

// readinessTimeout bounds one /readyz evaluation.
//
// The kubelet probe allows slightly longer (see the dashboard service deployment), so a dependency
// that is merely slow is reported by the handler with the reason attached, rather than recorded as
// an unexplained probe timeout.
const readinessTimeout = 5 * time.Second

// dataEngineStartupTimeout bounds the optional first ClickHouse dial made during startup.
//
// The startup context has no deadline of its own, so an unreachable ClickHouse that accepts the
// connection and then stalls would hang the whole process before it ever binds a port. The dial is
// only an optimisation — the provider retries on demand — so it is given a short budget and the
// service comes up regardless.
const dataEngineStartupTimeout = 10 * time.Second

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

func initAuthenticator(ctx context.Context, projectID string) (*oauth2.Authenticator, *oauth2.Config, error) {
	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load OAuth config: %w", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = projectID
	}
	return oauth2.NewAuthenticator(oauthCfg), oauthCfg, nil
}

// buildClickHouseConnector constructs the connector the dataengine Provider uses on each dial.
func buildClickHouseConnector(enableComponents bool, namespace string) dataengine.Connector {
	if !enableComponents {
		return connectClickHouseWithSource(componentconfig.Source{Enabled: false}, nil)
	}
	var (
		mu      sync.Mutex
		getter  crreader.Getter
		secrets crreader.SecretReader
	)
	return func(ctx context.Context) (dataengine.ClickHouseConn, error) {
		mu.Lock()
		if getter == nil {
			g, err := crreader.NewK8sGetter()
			if err != nil {
				mu.Unlock()
				return nil, fmt.Errorf("failed to initialize component CR getter: %w", err)
			}
			getter = g
		}
		if secrets == nil {
			s, err := crreader.NewK8sSecretReader()
			if err != nil {
				mu.Unlock()
				return nil, fmt.Errorf("failed to initialize kubernetes secret reader: %w", err)
			}
			secrets = s
		}
		g, s := getter, secrets
		mu.Unlock()

		return connectClickHouseWithSource(componentconfig.Source{
			Enabled:   true,
			Namespace: namespace,
			Getter:    g,
			Secrets:   s,
		}, nil)(ctx)
	}
}

func connectClickHouseWithSource(src componentconfig.Source, initErr error) dataengine.Connector {
	return func(ctx context.Context) (dataengine.ClickHouseConn, error) {
		if initErr != nil {
			return nil, initErr
		}
		opts, err := componentconfig.ResolveOptions(ctx, src)
		if err != nil {
			return nil, err
		}
		return clickhouse.ConnectWithOptions(ctx, opts)
	}
}

// initDataEngine prepares the chart data engine, for deployments that configure ClickHouse.
//
// A failed first attempt is not fatal. The provider dials again on demand, and readiness holds the
// pod out of the service endpoints until one of those attempts succeeds.
func initDataEngine(ctx context.Context) *dataengine.Provider {
	if !clickhouse.Configured() && !cfg.EnableComponents {
		logging.Info(ctx, "ClickHouse is not configured, starting without a chart data engine")
		return nil
	}
	provider := dataengine.NewProvider(buildClickHouseConnector(cfg.EnableComponents, cfg.ComponentsNamespace))
	dialCtx, cancel := context.WithTimeout(ctx, dataEngineStartupTimeout)
	defer cancel()
	if _, err := provider.Engine(dialCtx); err != nil {
		logging.Warn(ctx, "ClickHouse connection not available at startup, the data engine is retried on demand", "error", err)
	}
	return provider
}

// chartDataEngine converts the engine provider into the interface the server consumes.
//
// The conversion cannot be left implicit: assigning a nil *Provider straight into the interface
// yields a non-nil interface holding a nil pointer, which passes the server's "data engine is not
// configured" guard and then dereferences nil on the first method call.
func chartDataEngine(provider *dataengine.Provider) server.ChartDataEngine {
	if provider == nil {
		return nil
	}
	return provider
}

// newReadinessCheck builds the /readyz check over the dependencies this deployment configured.
//
// Failing it removes the pod from the service endpoints, so only dependencies the pod cannot
// usefully serve without belong here. A release that configures ClickHouse expects chart data to
// work, which makes an unbuildable data engine disqualifying; a release without ClickHouse passes
// no provider and is unaffected.
func newReadinessCheck(sc *spanner.Client, provider *dataengine.Provider) func(context.Context) error {
	if sc == nil && provider == nil {
		return nil
	}
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
		defer cancel()

		if sc != nil {
			if err := commonhealth.CheckSpanner(checkCtx, sc); err != nil {
				return err
			}
		}
		if provider == nil {
			return nil
		}
		// Ping rather than Engine: the engine is built once and cached, so asking only whether one
		// exists keeps reporting ready long after ClickHouse has gone away. The probe has to reach
		// the database itself for its answer to mean anything.
		if err := provider.Ping(checkCtx); err != nil {
			return fmt.Errorf("chart data engine is unavailable: %w", err)
		}
		return nil
	}
}

func initSpannerClient(ctx context.Context, databaseName string, projectID string) (*spanner.Client, error) {
	if databaseName == "" {
		return nil, errors.New("spanner_database_name is required but not configured")
	}
	sessionPoolConfig := spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}
	sc, err := spanner.NewClientWithConfig(
		ctx,
		databaseName,
		spanner.ClientConfig{SessionPoolConfig: sessionPoolConfig},
		option.WithQuotaProject(projectID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client: %w", err)
	}
	return sc, nil
}

func initRepository(ctx context.Context, sc *spanner.Client) (server.DashboardRepository, error) {
	spannerRepo, err := dashboard.NewRepository(sc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize dashboard repository: %w", err)
	}
	return spannerRepo, nil
}

func initAuthorizer(ctx context.Context, projectID string, oauthCfg *oauth2.Config) auth.AuthorizerInterface {
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(projectID))
	if err != nil {
		logging.Info(ctx, "Cloud Identity service not available at startup", "error", err)
	}
	authorizer, err := auth.NewAuthorizer(
		ctx,
		idService,
		10*time.Minute,
		1*time.Minute,
		[]string{oauthCfg.ClientID},
		auth.DefaultIAPIssuer,
		[]byte(oauthCfg.JWTSecret),
	)
	if err != nil {
		logging.Info(ctx, "Authorizer initialized without full IAP configuration", "error", err)
	}
	return authorizer
}

func initXidClient(addr string) (xidapipb.XidServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to XID service at %q: %w", addr, err)
	}
	return xidapipb.NewXidServiceClient(conn), conn, nil
}

func initGRPCServer(ctx context.Context, cfg *server.Config, repo server.DashboardRepository, authorizer auth.AuthorizerInterface, xidClient xidapipb.XidServiceClient, de *dataengine.Provider, errChan chan<- error) (*grpc.Server, net.Listener, error) {
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on gRPC port: %w", err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterDashboardServiceServer(grpcServer, server.NewServer(cfg, repo, authorizer, xidClient, chartDataEngine(de)))
	reflection.Register(grpcServer)

	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("xmc.DashboardService", healthpb.HealthCheckResponse_SERVING)

	go func() {
		logging.Info(ctx, "Dashboard service gRPC server starting", "addr", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			errChan <- fmt.Errorf("gRPC server failed: %w", err)
		}
	}()

	return grpcServer, lis, nil
}

func initHTTPServer(ctx context.Context, cfg *server.Config, authenticator *oauth2.Authenticator, sc *spanner.Client, de *dataengine.Provider, errChan chan<- error) (*http.Server, error) {
	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err := spb.RegisterDashboardServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to register gateway: %w", err)
	}

	handler := setupHTTPHandler(authenticator, gwmux, newReadinessCheck(sc, de))

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

	return httpServer, nil
}

func gracefulShutdown(ctx context.Context, httpServer *http.Server, grpcServer *grpc.Server, quit <-chan os.Signal, errChan <-chan error) {
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

func main() {
	os.Exit(run())
}

func run() int {
	google.Init()
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	authenticator, oauthCfg, err := initAuthenticator(ctx, cfg.ProjectID)
	if err != nil {
		logging.Error(ctx, "Failed to initialize authenticator", "error", err)
		return 1
	}
	de := initDataEngine(ctx)

	spannerDB := getSpannerDatabaseName()
	sc, err := initSpannerClient(ctx, spannerDB, cfg.ProjectID)
	if err != nil {
		logging.Error(ctx, "Failed to initialize Spanner client", "error", err)
		return 1
	}
	defer sc.Close()

	repo, err := initRepository(ctx, sc)
	if err != nil {
		logging.Error(ctx, "Failed to initialize dashboard repository", "error", err)
		return 1
	}
	authorizer := initAuthorizer(ctx, cfg.ProjectID, oauthCfg)

	xidClient, xidConn, err := initXidClient(cfg.XidServiceAddr)
	if err != nil {
		logging.Error(ctx, "Failed to initialize XID service client", "error", err)
		return 1
	}
	defer xidConn.Close()

	errChan := make(chan error, 2)
	grpcServer, lis, err := initGRPCServer(ctx, cfg, repo, authorizer, xidClient, de, errChan)
	if err != nil {
		logging.Error(ctx, "Failed to initialize gRPC server", "error", err)
		return 1
	}
	defer lis.Close()

	httpServer, err := initHTTPServer(ctx, cfg, authenticator, sc, de, errChan)
	if err != nil {
		logging.Error(ctx, "Failed to initialize HTTP server", "error", err)
		return 1
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
	return 0
}
