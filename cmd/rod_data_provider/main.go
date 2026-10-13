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
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/option"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/cmd/rod_data_provider/reconciler"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/statushistory"
	"xmanager_cloud/internal/storage/spanner/workunit"
	"xmanager_cloud/pkg/auth/oauth2"
)

var _ QueueVelocityFetcher = (*statushistory.Repository)(nil)

func main() {
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	rootCtx := logging.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(rootCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		logging.Fatal(ctx, "ROD data provider failed", "error", err)
	}
}

func run(ctx context.Context, cfg *Config) error {
	kueueCache := cache.NewKueueCache()
	mgr, err := setupKueueManager(ctx, kueueCache)
	if err != nil {
		logging.Warn(ctx, "Kueue manager setup failed or running out-of-cluster", "error", err)
	}

	workUnitStore, velocityStore, err := initStorage(ctx, cfg)
	if err != nil {
		return err
	}

	grpcComp, err := buildGRPCServer(cfg, kueueCache, workUnitStore, velocityStore)
	if err != nil {
		return fmt.Errorf("failed to build gRPC server: %w", err)
	}
	lis, grpcServer := grpcComp.Listener, grpcComp.Server

	httpServer, err := buildHTTPServer(ctx, cfg, kueueCache, mgr)
	if err != nil {
		return fmt.Errorf("failed to build HTTP server: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)

	if mgr != nil {
		g.Go(func() error {
			logging.Info(ctx, "Starting controller-runtime Manager for KueueCache")
			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("controller-runtime manager error: %w", err)
			}
			return nil
		})
	}

	g.Go(func() error {
		logging.Info(ctx, "ROD Data Provider gRPC starting", "addr", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			return fmt.Errorf("gRPC server error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		logging.Info(ctx, "ROD Data Provider HTTP gateway starting", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTP server error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		performShutdown(ctx, httpServer, grpcServer)
		return nil
	})

	return g.Wait()
}

func initStorage(ctx context.Context, cfg *Config) (WorkUnitFetcher, QueueVelocityFetcher, error) {
	if cfg.SpannerDatabaseName == "" {
		logging.Warn(ctx, "OPERATIONAL WARNING: Spanner database name not configured. Active WorkUnit queue queries will fail until database connection is configured.", "database", cfg.SpannerDatabaseName)
		return nil, nil, nil
	}
	logging.Info(ctx, "Initializing Spanner WorkUnit storage", "database", cfg.SpannerDatabaseName)
	var opts []option.ClientOption
	if cfg.QuotaProjectID != "" {
		opts = append(opts, option.WithQuotaProject(cfg.QuotaProjectID))
	}
	spannerClient, err := spanner.NewClient(ctx, cfg.SpannerDatabaseName, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create spanner client for WorkUnit storage: %w", err)
	}
	wuStore, err := workunit.NewRepository(spannerClient, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create WorkUnit repository: %w", err)
	}
	statusHistoryRepo := statushistory.NewRepository(spannerClient)
	return wuStore, statusHistoryRepo, nil
}

// GRPCServerComponents contains the listener and gRPC server instance.
type GRPCServerComponents struct {
	Listener net.Listener
	Server   *grpc.Server
}

func buildGRPCServer(cfg *Config, kueueCache *cache.KueueCache, workUnitStore WorkUnitFetcher, velocityStore QueueVelocityFetcher) (GRPCServerComponents, error) {
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return GRPCServerComponents{}, fmt.Errorf("failed to listen on gRPC port %s: %w", cfg.GRPCPort, err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterResourceObservabilityServiceServer(grpcServer, newServer(cfg, kueueCache, workUnitStore, velocityStore))
	reflection.Register(grpcServer)
	return GRPCServerComponents{
		Listener: lis,
		Server:   grpcServer,
	}, nil
}

func buildHTTPServer(ctx context.Context, cfg *Config, kueueCache *cache.KueueCache, mgr ctrl.Manager) (*http.Server, error) {
	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load OAuth config: %w", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)

	gwmux := gwruntime.NewServeMux(gwruntime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err = spb.RegisterResourceObservabilityServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to register gateway endpoint: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if mgr != nil && mgr.GetCache() != nil {
			if !mgr.GetCache().WaitForCacheSync(r.Context()) {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Kueue cache sync pending"))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	authenticatedGwmux := authenticator.Authenticate(gwmux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ROD Data Provider OK"))
			return
		}
		authenticatedGwmux.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%s", cfg.Port)
	handler := authenticator.SecurityMiddlewares(mux)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}, nil
}

func performShutdown(ctx context.Context, httpServer *http.Server, grpcServer *grpc.Server) {
	logging.Info(ctx, "Shutting down ROD data provider service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logging.Error(ctx, "Error shutting down HTTP server", "error", err)
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

	logging.Info(ctx, "ROD data provider service stopped")
}

func setupKueueManager(ctx context.Context, kueueCache *cache.KueueCache) (ctrl.Manager, error) {
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s config for KueueCache reconciler: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add client-go scheme: %w", err)
	}
	if err := kueuev1beta1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add Kueue v1beta1 scheme: %w", err)
	}

	metricsServerOptions := metricsserver.Options{
		BindAddress: "0",
	}
	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		Scheme:         scheme,
		Metrics:        metricsServerOptions,
		LeaderElection: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime manager: %w", err)
	}

	if err := SetupKueueReconcilers(mgr, mgr.GetClient(), kueueCache); err != nil {
		return nil, fmt.Errorf("failed to setup Kueue reconcilers: %w", err)
	}

	return mgr, nil
}

// SetupKueueReconcilers orchestrates ClusterQueue, LocalQueue, and ResourceFlavor reconcilers
// to collect and sync Kueue entity information into the in-memory cache.
func SetupKueueReconcilers(mgr ctrl.Manager, client client.Client, cache *cache.KueueCache) error {
	if cache == nil || cache.ClusterQueues == nil || cache.LocalQueues == nil || cache.ResourceFlavors == nil {
		return fmt.Errorf("kueue cache or its fields are uninitialized")
	}
	cqRec := reconciler.NewClusterQueueReconciler(client, cache.ClusterQueues)
	if err := cqRec.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ClusterQueueReconciler: %w", err)
	}
	lqRec := reconciler.NewLocalQueueReconciler(client, cache.LocalQueues)
	if err := lqRec.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup LocalQueueReconciler: %w", err)
	}
	rfRec := reconciler.NewResourceFlavorReconciler(client, cache.ResourceFlavors)
	if err := rfRec.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceFlavorReconciler: %w", err)
	}
	return nil
}
