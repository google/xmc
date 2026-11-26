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

// Package main is the entrypoint of xmc-study-controller, the workload of the hpo component.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"xmanager_cloud/cmd/study_controller/config"
	"xmanager_cloud/cmd/study_controller/essclient"
	"xmanager_cloud/cmd/study_controller/worker"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/study/vizier"
)

// HTTP server timeouts bound per-connection resource usage (e.g. slow-client attacks).
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// run parses args, then serves the HTTP endpoints until ctx is done. It returns the process exit
// code.
func run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("study_controller", flag.ContinueOnError)
	cfg := config.RegisterFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger := logging.NewGCPLogger(slog.LevelInfo, "")
	slog.SetDefault(logger)
	ctx = logging.WithLogger(ctx, logger)

	if err := cfg.Validate(); err != nil {
		logging.Error(ctx, "Invalid configuration", "error", err)
		return 1
	}
	if err := listenAndServe(ctx, *cfg); err != nil {
		logging.Error(ctx, "Study controller stopped with error", "error", err)
		return 1
	}
	return 0
}

// listenAndServe serves HTTP endpoints, resolves Vizier, and consumes study reconcile ticks until ctx is done.
func listenAndServe(ctx context.Context, cfg config.Config) error {
	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", cfg.Port, err)
	}
	essClient, err := essclient.New(ctx, cfg.ESSAddr, cfg.ESSAudience)
	if err != nil {
		_ = lis.Close()
		return fmt.Errorf("failed to create ESS client: %w", err)
	}
	defer func() {
		if closeErr := essClient.Close(); closeErr != nil {
			logging.Warn(ctx, "Failed to close ESS client", "error", closeErr)
		}
	}()
	newReconciler := func(vc *vizier.Client) (worker.ReconcileFunc, error) {
		engine, err := vizier.NewEngine(vc)
		if err != nil {
			return nil, fmt.Errorf("failed to create Vizier engine: %w", err)
		}
		return worker.NewESSReconciler(essClient, engine)
	}
	return serveWithClient(ctx, lis, worker.NewRedisConsumer(cfg.RedisAddr), newReconciler, cfg.ComponentsNamespace, crreader.NewK8sGetter)
}

func serveWithClient(
	ctx context.Context,
	lis net.Listener,
	client *redisclient.ConsumerClient,
	newReconciler func(*vizier.Client) (worker.ReconcileFunc, error),
	componentsNamespace string,
	newGetter func() (crreader.Getter, error),
) error {
	if _, err := worker.NewRedisHandler(client, nil); err != nil {
		_ = lis.Close()
		closeRedisClient(ctx, client)
		return fmt.Errorf("failed to create study reconcile handler: %w", err)
	}
	vp := &vizierProbe{}
	httpCtx, cancelHTTP := context.WithCancel(ctx)
	defer cancelHTTP()
	resolveCtx, cancelResolve := context.WithCancel(ctx)
	defer cancelResolve()
	serveErr := make(chan error, 1)
	go func() {
		defer cancelResolve()
		serveErr <- serve(httpCtx, lis, newHTTPHandler(client, vp))
	}()

	vizierClient, err := resolveAndDialVizier(resolveCtx, componentsNamespace, newGetter)
	if err != nil {
		cancelHTTP()
		sErr := <-serveErr
		closeRedisClient(ctx, client)
		if sErr != nil || ctx.Err() == nil {
			return cmp.Or(sErr, err)
		}
		return nil
	}
	defer func() {
		if err := vizierClient.Close(); err != nil {
			logging.Warn(ctx, "Failed to close Vizier client", "error", err)
		}
	}()

	var reconcile worker.ReconcileFunc
	if newReconciler != nil {
		if reconcile, err = newReconciler(vizierClient); err != nil {
			stopServing(ctx, client, cancelHTTP, serveErr)
			return fmt.Errorf("failed to create ESS reconciler: %w", err)
		}
	}
	handler, err := worker.NewRedisHandler(client, reconcile)
	if err != nil {
		stopServing(ctx, client, cancelHTTP, serveErr)
		return fmt.Errorf("failed to create study reconcile handler: %w", err)
	}
	vp.Set(vizierClient)

	consumer := worker.Start(ctx, client, handler)
	defer stopConsumer(ctx, consumer)
	return <-serveErr
}

func stopServing(ctx context.Context, client *redisclient.ConsumerClient, cancel context.CancelFunc, serveErr <-chan error) {
	cancel()
	<-serveErr
	closeRedisClient(ctx, client)
}

func closeRedisClient(ctx context.Context, client *redisclient.ConsumerClient) {
	if client != nil && client.Client != nil {
		_ = client.Close(context.WithoutCancel(ctx))
	}
}

func resolveAndDialVizier(ctx context.Context, ns string, newGetter func() (crreader.Getter, error)) (*vizier.Client, error) {
	if newGetter == nil {
		return nil, errors.New("component getter constructor must not be nil")
	}
	getter, err := newGetter()
	if err != nil {
		return nil, fmt.Errorf("failed to create component getter: %w", err)
	}
	endpoint, err := vizier.WaitForEndpoint(ctx, getter, ns)
	if err != nil {
		return nil, err
	}
	return vizier.Dial(endpoint)
}

// stopConsumer stops consumer, logging rather than returning a failure: it runs on shutdown.
func stopConsumer(ctx context.Context, consumer *worker.Consumer) {
	if err := consumer.Close(context.WithoutCancel(ctx)); err != nil {
		logging.Warn(ctx, "Failed to close the study reconcile consumer", "error", err)
	}
}

// serve serves handler on lis until ctx is done, then shuts the server down gracefully.
func serve(ctx context.Context, lis net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()
	logging.Info(ctx, "Study controller listening", "addr", lis.Addr().String())

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server failed: %w", err)
	case <-ctx.Done():
	}

	logging.Info(ctx, "Shutting down study controller gracefully")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown failed: %w", err)
	}
	return nil
}
