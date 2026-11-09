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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/api/cloudidentity/v1beta1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	spb "xmanager_cloud/api/protos/dashboard"
	xidapipb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/dashboard_service/server"
	"xmanager_cloud/internal/auth"
	commonhealth "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/dbconfig"
	dbfactory "xmanager_cloud/internal/storage/factory"
	"xmanager_cloud/pkg/auth/oauth2"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
	bqconfig "xmanager_cloud/pkg/bigquery/componentconfig"
	"xmanager_cloud/pkg/clickhouse"
	"xmanager_cloud/pkg/clickhouse/componentconfig"
	"xmanager_cloud/pkg/components/crreader"
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
	authenticator := oauth2.NewAuthenticator(oauthCfg)
	return authenticator, oauthCfg, nil
}

type lazySecretReader struct {
	mu     sync.Mutex
	reader crreader.SecretReader
}

func (l *lazySecretReader) GetSecretValue(ctx context.Context, namespace, name, key string) (string, error) {
	l.mu.Lock()
	if l.reader == nil {
		r, err := crreader.NewK8sSecretReader()
		if err != nil {
			l.mu.Unlock()
			return "", fmt.Errorf("failed to initialize kubernetes secret reader: %w", err)
		}
		l.reader = r
	}
	reader := l.reader
	l.mu.Unlock()
	return reader.GetSecretValue(ctx, namespace, name, key)
}

// lazyComponentGetter caches an on-demand Kubernetes Component CR getter across readiness checks.
type lazyComponentGetter struct {
	mu     sync.Mutex
	getter crreader.Getter
}

func (l *lazyComponentGetter) get() (crreader.Getter, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.getter != nil {
		return l.getter, nil
	}
	g, err := crreader.NewK8sGetter()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize component CR getter: %w", err)
	}
	l.getter = g
	return g, nil
}

// buildClickHouseResolver constructs the dynamic resolver the dataengine Provider uses on dial and readiness checks.
func buildClickHouseResolver(enableComponents bool, namespace string, customGetter crreader.Getter) dataengine.Resolver {
	if !enableComponents {
		return resolveClickHouseWithSource(componentconfig.Source{Enabled: false}, nil)
	}
	getter := &lazyComponentGetter{getter: customGetter}
	secrets := &lazySecretReader{}
	return func(ctx context.Context) (string, dataengine.Connector, error) {
		g, err := getter.get()
		if err != nil {
			if clickhouse.Configured() {
				logging.Warn(ctx, "Kubernetes component client unavailable; falling back to static ClickHouse configuration", "error", err)
				return resolveClickHouseWithSource(componentconfig.Source{Enabled: false}, nil)(ctx)
			}
			return "", nil, err
		}
		src := componentconfig.Source{Enabled: true, Namespace: namespace, Getter: g, Secrets: secrets}
		return resolveClickHouseWithSource(src, nil)(ctx)
	}
}

func resolveClickHouseWithSource(src componentconfig.Source, initErr error) dataengine.Resolver {
	return resolveClickHouseWithDialer(src, initErr, func(ctx context.Context, opts *clickhouse.Options) (dataengine.ClickHouseConn, error) {
		return clickhouse.ConnectWithOptions(ctx, opts)
	})
}

func resolveClickHouseWithDialer(src componentconfig.Source, initErr error, dial func(context.Context, *clickhouse.Options) (dataengine.ClickHouseConn, error)) dataengine.Resolver {
	return func(ctx context.Context) (string, dataengine.Connector, error) {
		if initErr != nil {
			return "", nil, initErr
		}
		opts, err := componentconfig.ResolveOptions(ctx, src)
		if err != nil {
			return "", nil, err
		}
		return componentconfig.OptionsFingerprint(opts), func(dialCtx context.Context) (dataengine.ClickHouseConn, error) {
			return dial(dialCtx, opts)
		}, nil
	}
}

const (
	storeClickHouse = "clickhouse"
	storeBigQuery   = "bigquery"
)

func resolveMetricsStore(cfg *server.Config) (string, error) {
	raw := os.Getenv("METRICS_STORE")
	if cfg != nil && cfg.MetricsStore != "" {
		raw = cfg.MetricsStore
	}
	switch store := strings.ToLower(strings.TrimSpace(raw)); store {
	case "", storeClickHouse, storeBigQuery:
		return store, nil
	default:
		return "", fmt.Errorf("unsupported metrics_store %q: must be %q or %q", raw, storeClickHouse, storeBigQuery)
	}
}

func buildBigQueryResolver(enableComponents bool, namespace, projectID string, customGetter crreader.Getter) dataengine.EngineResolver {
	if !enableComponents {
		return resolveBigQueryWithSource(bqconfig.Source{Enabled: false, FallbackProjectID: projectID})
	}
	getter := &lazyComponentGetter{getter: customGetter}
	return func(ctx context.Context) (string, dataengine.EngineBuilder, error) {
		g, err := getter.get()
		if err != nil {
			if xmcbigquery.Configured() {
				logging.Warn(ctx, "Kubernetes component client unavailable; falling back to static BigQuery configuration", "error", err)
				return resolveBigQueryWithSource(bqconfig.Source{Enabled: false, FallbackProjectID: projectID})(ctx)
			}
			return "", nil, err
		}
		src := bqconfig.Source{Enabled: true, Namespace: namespace, FallbackProjectID: projectID, Getter: g}
		return resolveBigQueryWithSource(src)(ctx)
	}
}

func resolveBigQueryWithSource(src bqconfig.Source) dataengine.EngineResolver {
	return resolveBigQueryWithDialer(src, func(ctx context.Context, opts *xmcbigquery.Options) (xmcbigquery.Conn, error) {
		return xmcbigquery.ConnectWithOptions(ctx, opts)
	})
}

func resolveBigQueryWithDialer(src bqconfig.Source, dial func(context.Context, *xmcbigquery.Options) (xmcbigquery.Conn, error)) dataengine.EngineResolver {
	return func(ctx context.Context) (string, dataengine.EngineBuilder, error) {
		opts, err := bqconfig.ResolveOptions(ctx, src)
		if err != nil {
			return "", nil, err
		}
		return bqconfig.OptionsFingerprint(opts), func(dialCtx context.Context) (*dataengine.DataEngine, error) {
			conn, err := dial(dialCtx, opts)
			if err != nil {
				return nil, err
			}
			return dataengine.NewBigQuery(conn)
		}, nil
	}
}

// initDataEngine prepares the chart data engine, for deployments that configure ClickHouse or BigQuery.
//
// A failed first attempt is not fatal. The provider dials again on demand, and readiness holds the
// pod out of the service endpoints until one of those attempts succeeds.
func initDataEngine(ctx context.Context, cfg *server.Config) *dataengine.Provider {
	return initDataEngineWithGetter(ctx, cfg, nil)
}

func initDataEngineWithGetter(ctx context.Context, cfg *server.Config, customGetter crreader.Getter) *dataengine.Provider {
	store, err := resolveMetricsStore(cfg)
	if err != nil {
		logging.Error(ctx, "Invalid metrics store configuration", "error", err)
		return nil
	}
	enableComponents, componentsNamespace, projectID := false, "xmc", ""
	if cfg != nil {
		enableComponents, componentsNamespace, projectID = cfg.EnableComponents, cfg.ComponentsNamespace, cfg.ProjectID
	}
	if store == storeBigQuery || (store == "" && !isClickHouseExpected(ctx, enableComponents, componentsNamespace, customGetter) && isBigQueryExpected(ctx, enableComponents, componentsNamespace, customGetter)) {
		return warmProvider(ctx, "BigQuery", dataengine.NewDynamicEngineProvider(buildBigQueryResolver(enableComponents, componentsNamespace, projectID, customGetter)))
	}
	if !isClickHouseExpected(ctx, enableComponents, componentsNamespace, customGetter) {
		logging.Info(ctx, "ClickHouse is not configured, starting without a chart data engine")
		return nil
	}
	return warmProvider(ctx, "ClickHouse", dataengine.NewDynamicProvider(buildClickHouseResolver(enableComponents, componentsNamespace, customGetter)))
}

func warmProvider(ctx context.Context, name string, provider *dataengine.Provider) *dataengine.Provider {
	dialCtx, cancel := context.WithTimeout(ctx, dataEngineStartupTimeout)
	defer cancel()
	if _, err := provider.Engine(dialCtx); err != nil {
		logging.Warn(ctx, name+" connection not available at startup, the data engine is retried on demand", "error", err)
	}
	return provider
}

func isClickHouseExpected(ctx context.Context, enableComponents bool, namespace string, customGetter crreader.Getter) bool {
	return clickhouse.Configured() || (enableComponents && isComponentExpected(ctx, namespace, componentconfig.ClickHouseComponentName, customGetter))
}

func isBigQueryExpected(ctx context.Context, enableComponents bool, namespace string, customGetter crreader.Getter) bool {
	return xmcbigquery.Configured() || (enableComponents && isComponentExpected(ctx, namespace, bqconfig.BigQueryComponentName, customGetter))
}

func isComponentExpected(ctx context.Context, namespace, componentName string, customGetter crreader.Getter) bool {
	getter := customGetter
	if getter == nil {
		g, err := crreader.NewK8sGetter()
		if err != nil {
			return false
		}
		getter = g
	}
	ns := crreader.ResolveNamespace(namespace)
	probeCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	comp, err := getter.GetComponent(probeCtx, ns, componentName)
	if apierrors.IsNotFound(err) || (err == nil && (comp == nil || crreader.IsDisabled(comp))) {
		return false
	}
	if err != nil {
		logging.Warn(ctx, "Failed to inspect Component CR at startup; assuming component is expected", "namespace", ns, "component", componentName, "error", err)
	}
	return true
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
func newReadinessCheck(db commonhealth.ReadinessChecker, provider *dataengine.Provider) func(context.Context) error {
	if db == nil && provider == nil {
		return nil
	}
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
		defer cancel()

		if db != nil {
			if err := db.CheckReady(checkCtx); err != nil {
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

// initAuthorizer builds the gRPC authorizer. admission must come from the same oauth2.Authenticator
// the HTTP middleware uses, so gRPC and HTTP admit exactly the same principals.
func initAuthorizer(ctx context.Context, projectID string, oauthCfg *oauth2.Config, admission auth.AdmissionChecker) (auth.AuthorizerInterface, error) {
	idService, err := cloudidentity.NewService(ctx, option.WithQuotaProject(projectID))
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud identity service: %w", err)
	}
	authorizer, err := auth.NewAuthorizer(auth.AuthorizerParams{
		IDService:     idService,
		CacheTTL:      10 * time.Minute,
		CacheErrorTTL: time.Minute,
		IAPAudiences:  []string{oauthCfg.ClientID},
		IAPIssuer:     auth.DefaultIAPIssuer,
		JWTSecret:     []byte(oauthCfg.JWTSecret),
		Admission:     admission,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}
	return authorizer, nil
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
	healthpb.RegisterHealthServer(grpcServer, healthServer)
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

func initHTTPServer(ctx context.Context, cfg *server.Config, authenticator *oauth2.Authenticator, db commonhealth.ReadinessChecker, de *dataengine.Provider, errChan chan<- error) (*http.Server, error) {
	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err := spb.RegisterDashboardServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to register gateway: %w", err)
	}

	handler := setupHTTPHandler(authenticator, gwmux, newReadinessCheck(db, de))

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
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)
	if _, err := resolveMetricsStore(cfg); err != nil {
		logging.Error(ctx, "Failed to validate metrics store configuration", "error", err)
		return 1
	}

	authenticator, oauthCfg, err := initAuthenticator(ctx, cfg.ProjectID)
	if err != nil {
		logging.Error(ctx, "Failed to initialize authenticator", "error", err)
		return 1
	}
	defer authenticator.Close()
	de := initDataEngine(ctx, cfg)
	if de != nil {
		defer de.Close()
	}

	dbCfg, err := dbconfig.Load()
	if err != nil {
		logging.Error(ctx, "Failed to load database configuration", "error", err)
		return 1
	}
	storage, err := dbfactory.NewStorage(ctx, dbCfg, dbfactory.ESS)
	if err != nil {
		logging.Error(ctx, "Failed to initialize storage", "error", err)
		return 1
	}
	defer storage.Close()

	authorizer, err := initAuthorizer(ctx, cfg.ProjectID, oauthCfg, auth.DeploymentAdmission(authenticator))
	if err != nil {
		logging.Error(ctx, "Failed to initialize authorizer", "error", err)
		return 1
	}

	xidClient, xidConn, err := initXidClient(cfg.XidServiceAddr)
	if err != nil {
		logging.Error(ctx, "Failed to initialize XID service client", "error", err)
		return 1
	}
	defer xidConn.Close()

	errChan := make(chan error, 2)
	grpcServer, lis, err := initGRPCServer(ctx, cfg, storage, authorizer, xidClient, de, errChan)
	if err != nil {
		logging.Error(ctx, "Failed to initialize gRPC server", "error", err)
		return 1
	}
	defer lis.Close()

	httpServer, err := initHTTPServer(ctx, cfg, authenticator, storage, de, errChan)
	if err != nil {
		logging.Error(ctx, "Failed to initialize HTTP server", "error", err)
		return 1
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
	return 0
}
