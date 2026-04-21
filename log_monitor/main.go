// Package main implements the Log Monitor for XManager Cloud.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"go/google"
	"golang/cloud_google_com/go/pubsub/v/v1/pubsub"
	"xmanager_cloud/logging/logging"
)

func main() {
	google.Init()

	cfg, err := LoadConfig()
	if err != nil {
		logging.Fatal(context.Background(), "Failed to load config", "error", err)
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.ProjectID)
	slog.SetDefault(logger)
	ctx := logging.WithLogger(context.Background(), logger)

	stg, sc, cleanup, err := newStorage(ctx, cfg)
	if err != nil {
		logging.Fatal(ctx, "Failed to create storage", "error", err)
	}
	defer cleanup()

	pubsubClient, err := pubsub.NewClient(ctx, cfg.ProjectID)
	if err != nil {
		logging.Fatal(ctx, "Failed to create Pub/Sub client", "error", err)
	}
	defer pubsubClient.Close()

	logProcessor := NewLogProcessor(stg, cfg.LogKeyword, cfg.MaxConcurrentUpdates)

	listener := NewListener(cfg, logProcessor, pubsubClient)

	// Start listener in a goroutine
	// We use a cancellable context for the listener to stop it gracefully
	listenerCtx, cancelListener := context.WithCancel(ctx)
	defer cancelListener()

	var listenerWg sync.WaitGroup
	listenerWg.Add(1)
	go func() {
		defer listenerWg.Done()
		listener.Start(listenerCtx)
	}()

	// HTTP server for health checks
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", activeHealthCheck(sc, listener))

	addr := fmt.Sprintf(":%s", cfg.Port)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		logging.Info(ctx, "Health check server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Fatal(ctx, "HTTP server failed", "error", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logging.Info(ctx, "Shutting down...")

	logging.Info(ctx, "Cancelling listener context...")
	cancelListener()

	logging.Info(ctx, "Waiting for listener to shut down fully...")
	listenerWg.Wait()
	logging.Info(ctx, "Listener goroutine finished.")

	// Close HTTP server
	if err := server.Shutdown(ctx); err != nil {
		logging.Fatal(ctx, "Error shutting down HTTP server", "error", err)
	}
}
