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
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"xmanager_cloud/internal/logging"
)

// readinessPingTimeout bounds the Redis and Vizier checks of one readiness probe. It is shorter
// than the chart's readinessProbe timeoutSeconds, so a hung dependency yields a 503 rather than a
// probe timeout.
const readinessPingTimeout = 2 * time.Second

// pinger reports whether a dependency is reachable.
type pinger interface {
	Ping(ctx context.Context) error
}

// vizierProbe holds the latched Vizier client once startup Component CR resolution succeeds.
type vizierProbe struct {
	mu     sync.RWMutex
	client pinger
}

func (v *vizierProbe) Set(c pinger) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.client = c
}

func (v *vizierProbe) Ping(ctx context.Context) error {
	v.mu.RLock()
	c := v.client
	v.mu.RUnlock()
	if c == nil {
		return errors.New("vizier endpoint not yet resolved")
	}
	return c.Ping(ctx)
}

// newHTTPHandler returns the mux serving the liveness, readiness and Prometheus metrics endpoints.
//
// Liveness only reports that the process serves requests, so a dependency outage never restarts the
// pod. Readiness additionally requires both Redis and Vizier to be reachable.
func newHTTPHandler(redis, vizier pinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", writeOK)
	mux.HandleFunc("GET /readyz", readinessCheck(redis, vizier))
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// readinessCheck returns a handler that responds 200 OK when both redis and vizier answer within
// readinessPingTimeout, and 503 Service Unavailable otherwise. The cause is logged, not returned.
func readinessCheck(redis, vizier pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
		defer cancel()
		if err := redis.Ping(ctx); err != nil {
			logging.Warn(ctx, "Readiness check failed: Redis is unreachable", "error", err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		if vizier == nil {
			logging.Warn(ctx, "Readiness check failed: Vizier probe is not configured")
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := vizier.Ping(ctx); err != nil {
			logging.Warn(ctx, "Readiness check failed: Vizier is unreachable", "error", err)
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
