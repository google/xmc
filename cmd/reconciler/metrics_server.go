// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"net/http"
	"time"

	"golang/prometheus/client/prometheus/prometheus"
	"golang/prometheus/client/prometheus/promhttp/promhttp"
	"xmanager_cloud/logging/logging"
)

// metricsAddr is the dedicated port for the reconciler's Prometheus scrape
// endpoint. It is intentionally distinct from the controller-runtime manager's
// own metrics port (":8081"): that server exposes only the controller-runtime
// registry, whereas this endpoint exposes the global default registry, where the
// reconciler's own metrics (registered via promauto) live. PodMonitoring scrapes
// this port.
const metricsAddr = ":8082"

// newMetricsHandler returns an HTTP handler that exposes the union of the given
// gatherers. It is kept separate from the server wiring so the gathering
// behavior can be unit tested with arbitrary registries.
func newMetricsHandler(gatherers ...prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(prometheus.Gatherers(gatherers), promhttp.HandlerOpts{})
}

// serveMetrics runs an HTTP server that exposes the global default registry on a
// dedicated port. This is necessary because the manager's built-in metrics
// server serves only the controller-runtime registry, so the reconciler's own
// metrics — registered via promauto to the default registry — would otherwise
// never be scraped. The server runs until ctx is cancelled.
//
// Only the default registry is served (not merged with the controller-runtime
// registry) because both register the standard Go and process collectors, and
// gathering them together yields duplicate series that fail the scrape.
func serveMetrics(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", newMetricsHandler(prometheus.DefaultGatherer))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	// Stop the server when the manager's context is cancelled so shutdown is
	// graceful rather than relying on process exit.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logging.Error(ctx, "Failed to shut down metrics server gracefully", "error", err)
		}
	}()

	logging.Info(ctx, "Starting metrics server", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logging.Error(ctx, "Metrics server failed", "error", err)
	}
}
