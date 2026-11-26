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
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
	"xmanager_cloud/internal/logging"
)

// readinessPingTimeout bounds the Redis and Vizier checks of one readiness probe (<3s probe timeout).
const readinessPingTimeout = 2 * time.Second

// pinger reports whether a dependency is reachable.
type pinger interface {
	Ping(ctx context.Context) error
}

// vizierProbe holds the latched Vizier client once startup Component CR resolution succeeds.
type vizierProbe struct{ client atomic.Value }

func (v *vizierProbe) Set(c pinger) {
	if v != nil && c != nil {
		v.client.Store(c)
	}
}

func (v *vizierProbe) Ping(ctx context.Context) error {
	if v == nil {
		return errors.New("vizier probe is nil")
	}
	if p, ok := v.client.Load().(pinger); ok && p != nil {
		return p.Ping(ctx)
	}
	return errors.New("vizier endpoint not yet resolved")
}

// newHTTPHandler returns the mux serving /healthz, /readyz (Redis and Vizier), and /metrics.
func newHTTPHandler(redis, vizier pinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", writeOK)
	mux.HandleFunc("GET /readyz", readinessCheck(redis, vizier))
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// readinessCheck responds 200 OK when Redis and Vizier answer concurrently within readinessPingTimeout, else 503.
func readinessCheck(redis, vizier pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
		defer cancel()
		g, pingCtx := errgroup.WithContext(ctx)
		for _, dep := range []struct {
			name string
			p    pinger
		}{{"Redis", redis}, {"Vizier", vizier}} {
			g.Go(func() error {
				if dep.p == nil {
					return fmt.Errorf("%s: probe not configured", dep.name)
				}
				if err := dep.p.Ping(pingCtx); err != nil {
					return fmt.Errorf("%s: %w", dep.name, err)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			logging.Warn(ctx, "Readiness check failed", "error", err)
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
