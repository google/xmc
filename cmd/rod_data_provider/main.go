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
	gwruntime "golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	"golang/grpc/reflection/reflection"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
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
	ctx := logging.WithLogger(context.Background(), logger)

	oauthCfg, err := oauth2.LoadConfig()
	if err != nil {
		logging.Fatal(ctx, "Failed to load OAuth config", "error", err)
	}
	if oauthCfg.GCPProjectID == "" {
		oauthCfg.GCPProjectID = cfg.ProjectID
	}
	authenticator := oauth2.NewAuthenticator(oauthCfg)

	kueueCache := cache.NewKueueCache()
	mgr, err := setupKueueManager(ctx, kueueCache)
	if err != nil {
		logging.Warn(ctx, "Kueue manager setup failed or running out-of-cluster", "error", err)
	}

	// 1. Setup gRPC server
	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logging.Fatal(ctx, "Failed to listen on gRPC port", "error", err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterResourceObservabilityServiceServer(grpcServer, newServer(cfg, kueueCache))
	reflection.Register(grpcServer)

	go func() {
		logging.Info(ctx, "ROD Data Provider gRPC starting", "addr", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			logging.Fatal(ctx, "gRPC server failed", "error", err)
		}
	}()

	// 2. Setup gRPC Gateway
	gwmux := gwruntime.NewServeMux(gwruntime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err = spb.RegisterResourceObservabilityServiceHandlerFromEndpoint(ctx, gwmux, "localhost:"+cfg.GRPCPort, dialOpts)
	if err != nil {
		logging.Fatal(ctx, "Failed to register gateway", "error", err)
	}

	// 3. Setup HTTP Server mux
	mux := http.NewServeMux()

	// Probes
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

	// Secure private gRPC-Gateway endpoints with Authenticate middleware
	authenticatedGwmux := authenticator.Authenticate(gwmux)

	// Route gateway endpoints
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

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	go func() {
		logging.Info(ctx, "ROD Data Provider HTTP gateway starting", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Fatal(ctx, "HTTP server failed", "error", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logging.Info(ctx, "Shutting down ROD data provider service...")
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logging.Info(ctx, "Error shutting down HTTP server", "error", err)
	}
	grpcServer.GracefulStop()

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

	go func() {
		logging.Info(ctx, "Starting controller-runtime Manager for KueueCache")
		if err := mgr.Start(ctx); err != nil {
			logging.Error(ctx, "controller-runtime manager error", "error", err)
		}
	}()

	return mgr, nil
}
