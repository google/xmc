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
	setupKueueManager(ctx, kueueCache)

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
	gwmux := gwruntime.NewServeMux()
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

// setupKueueManager initializes the Kubernetes controller-runtime manager and Kueue reconcilers if running in-cluster.
func setupKueueManager(ctx context.Context, kueueCache *cache.KueueCache) {
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		logging.Warn(ctx, "Failed to get in-cluster config for KueueCache reconciler", "error", err)
		return
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logging.Warn(ctx, "Failed to add client-go scheme", "error", err)
		return
	}
	if err := kueuev1beta1.AddToScheme(scheme); err != nil {
		logging.Warn(ctx, "Failed to add Kueue v1beta1 scheme", "error", err)
		return
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
		return
	}

	collector := &KueueCollector{
		Client: mgr.GetClient(),
		Cache:  kueueCache,
	}
	if err := collector.SetupWithManager(ctx, mgr); err != nil {
		logging.Warn(ctx, "Failed to setup KueueCollector with manager", "error", err)
		return
	}

	go func() {
		logging.Info(ctx, "Starting controller-runtime Manager for KueueCache")
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			logging.Error(ctx, "Manager failed to start", "error", err)
		}
	}()
}
