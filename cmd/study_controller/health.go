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
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"xmanager_cloud/internal/logging"
)

// readinessPingTimeout bounds the Redis ping of one readiness probe. It is shorter than the
// chart's readinessProbe timeoutSeconds, so a hung Redis yields a 503 rather than a probe timeout.
const readinessPingTimeout = 2 * time.Second

// pinger reports whether a dependency is reachable.
type pinger interface {
	Ping(ctx context.Context) error
}

// newHTTPHandler returns the mux serving the liveness, readiness and Prometheus metrics endpoints.
//
// Liveness only reports that the process serves requests, so a Redis outage never restarts the pod.
// Readiness additionally requires Redis to answer a ping.
func newHTTPHandler(redis pinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", writeOK)
	mux.HandleFunc("GET /readyz", readinessCheck(redis))
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// readinessCheck returns a handler that responds 200 OK when redis answers a ping within
// readinessPingTimeout, and 503 Service Unavailable otherwise. The cause is logged, not returned.
func readinessCheck(redis pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
		defer cancel()
		if err := redis.Ping(ctx); err != nil {
			logging.Warn(ctx, "Readiness check failed: Redis is unreachable", "error", err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		writeOK(w, r)
	}
}

// writeOK responds 200 OK with body "OK"; it backs the liveness and readiness probes.
func writeOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
