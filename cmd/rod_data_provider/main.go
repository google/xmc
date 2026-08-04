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

	"go/google"
	gwruntime "golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/go_sync/errgroup/errgroup"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	"golang/k8s_io/client_go/v/v0_23/rest/rest"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	metricsserver "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/metrics/server/server"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"
)

func main() {
	google.Init()
	flag.Parse()

	cfg := LoadConfig()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	ctx, stop := signal.NotifyContext(logging.WithLogger(context.Background(), logger), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	g, ctx := errgroup.WithContext(ctx)

	kueueCache := cache.NewKueueCache()
	mgr := setupKueueManager(ctx, g, kueueCache)

	lis, grpcServer, err := initGRPCServer(cfg, kueueCache)
	if err != nil {
		logging.Fatal(ctx, "Failed to initialize gRPC server", "error", err)
	}

	httpServer, err := initHTTPServer(ctx, cfg, kueueCache, mgr)
	if err != nil {
		logging.Fatal(ctx, "Failed to initialize HTTP gateway", "error", err)
	}

	g.Go(func() error { return serveGRPC(ctx, grpcServer, lis) })
	g.Go(func() error { return serveHTTP(ctx, httpServer) })

	if err := g.Wait(); err != nil {
		logging.Info(ctx, "ROD data provider stopped", "reason", err)
	}
}

func setupKueueManager(ctx context.Context, g *errgroup.Group, kueueCache *cache.KueueCache) ctrl.Manager {
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		logging.Warn(ctx, "Failed to get in-cluster config for KueueCache reconciler", "error", err)
		return nil
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logging.Warn(ctx, "Failed to add client-go scheme", "error", err)
		return nil
	}
	if err := kueuev1beta1.AddToScheme(scheme); err != nil {
		logging.Warn(ctx, "Failed to add Kueue v1beta1 scheme", "error", err)
		return nil
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
		logging.Warn(ctx, "Failed to create controller-runtime manager", "error", err)
		return nil
	}

	if err := SetupKueueReconcilers(mgr, mgr.GetClient(), kueueCache); err != nil {
		logging.Warn(ctx, "Failed to setup Kueue reconcilers with manager", "error", err)
		return nil
	}

	g.Go(func() error {
		logging.Info(ctx, "Starting controller-runtime Manager for KueueCache")
		if err := mgr.Start(ctx); err != nil {
			return fmt.Errorf("controller-runtime manager error: %w", err)
		}
		return nil
	})

	return mgr
}

func initGRPCServer(cfg *Config, kueueCache *cache.KueueCache) (net.Listener, *grpc.Server, error) {
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on gRPC port %s: %w", cfg.GRPCPort, err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterResourceObservabilityServiceServer(grpcServer, newServer(cfg, kueueCache))
	reflection.Register(grpcServer)
	return lis, grpcServer, nil
}

func serveGRPC(ctx context.Context, grpcServer *grpc.Server, lis net.Listener) error {
	logging.Info(ctx, "ROD Data Provider gRPC starting", "addr", lis.Addr())
	errCh := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logging.Info(ctx, "Stopping gRPC server gracefully...")
		done := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			logging.Warn(ctx, "gRPC server shutdown timed out, force stopping...")
			grpcServer.Stop()
		}
		return ctx.Err()
	}
}

func initHTTPServer(ctx context.Context, cfg *Config, kueueCache *cache.KueueCache, mgr ctrl.Manager) (*http.Server, error) {
	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load OAuth config: %w", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)

	gwmux := gwruntime.NewServeMux()
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if err := spb.RegisterResourceObservabilityServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts); err != nil {
		return nil, fmt.Errorf("failed to register gRPC-Gateway: %w", err)
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

func serveHTTP(ctx context.Context, httpServer *http.Server) error {
	logging.Info(ctx, "ROD Data Provider HTTP gateway starting", "addr", httpServer.Addr)
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logging.Info(ctx, "Stopping HTTP server gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logging.Error(ctx, "HTTP server shutdown error", "error", err)
		}
		return ctx.Err()
	}
}
