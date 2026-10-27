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

package metricsingester

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"xmanager_cloud/internal/logging"
)

// healthCheckTimeout bounds the metrics store readiness check made by the /readyz probe.
const healthCheckTimeout = 2 * time.Second

// Pinger checks whether the configured metrics store is reachable and ready.
type Pinger interface {
	Ping(ctx context.Context) error
}

// RunningReporter reports whether the receive loop is active. *Ingester satisfies it.
type RunningReporter interface {
	IsRunning() bool
}

// NewHealthMux serves /healthz (liveness) and /readyz (readiness).
//
// Liveness only checks the receive loop, so a metrics store outage takes the pod out of readiness
// without restarting it. Unwritten messages are nacked and redelivered once the store recovers.
func NewHealthMux(ingester RunningReporter, store Pinger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler(func(context.Context) error {
		return checkRunning(ingester)
	}))
	mux.HandleFunc("/readyz", healthHandler(func(ctx context.Context) error {
		if err := checkRunning(ingester); err != nil {
			return err
		}
		if err := store.Ping(ctx); err != nil {
			return fmt.Errorf("metrics store ping failed: %w", err)
		}
		return nil
	}))
	return mux
}

func checkRunning(ingester RunningReporter) error {
	if !ingester.IsRunning() {
		return errors.New("pub/sub receive loop is not running")
	}
	return nil
}

// healthHandler responds 200 when check passes and 503 otherwise.
func healthHandler(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		if err := check(ctx); err != nil {
			logging.Warn(ctx, "Health check failed", "path", r.URL.Path, "error", err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}
}
