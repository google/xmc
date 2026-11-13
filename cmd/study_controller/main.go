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
// It connects to Redis and serves health and metrics endpoints; /readyz reports whether Redis is
// reachable.
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

	"github.com/redis/go-redis/v9"
	"xmanager_cloud/cmd/study_controller/config"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
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

// listenAndServe listens on cfg.Port and serves the HTTP endpoints, with readiness backed by a
// Redis client for cfg.RedisAddr, until ctx is done. The Redis client is closed on return.
func listenAndServe(ctx context.Context, cfg config.Config) error {
	lis, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", cfg.Port, err)
	}
	client := newRedisClient(cfg.RedisAddr)
	defer closeRedisClient(ctx, client)
	return serve(ctx, lis, newHTTPHandler(redisPinger{client: client}))
}

// newRedisClient returns a client for the Redis server at addr. It reads the password from
// REDIS_PASSWORD and connects lazily, on the first command. Context deadlines bound socket I/O,
// so a Redis server that accepts connections but never replies fails a readiness ping after
// readinessPingTimeout rather than after the client's longer read timeout.
func newRedisClient(addr string) *redis.Client {
	return redisclient.NewClient(redisclient.Config{Addr: addr, ContextTimeoutEnabled: true})
}

// redisPinger adapts a go-redis client to the pinger interface of the readiness check.
type redisPinger struct {
	client *redis.Client
}

// Ping sends a Redis PING and returns its error, if any.
func (p redisPinger) Ping(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}

// closeRedisClient closes client, logging rather than returning a failure: it runs on shutdown.
func closeRedisClient(ctx context.Context, client *redis.Client) {
	if err := client.Close(); err != nil {
		logging.Warn(ctx, "Failed to close Redis client", "error", err)
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
