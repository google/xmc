package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	"xmanager_cloud/common/health"
	"xmanager_cloud/logging/logging"
)

// activeHealthCheck returns an HTTP handler that performs active dependency checks.
func activeHealthCheck(sc *spanner.Client, listener *Listener) http.HandlerFunc {
	const healthCheckTimeout = 2 * time.Second

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		var eg errgroup.Group

		// 1. Spanner check
		eg.Go(func() error {
			return health.CheckSpanner(ctx, sc)
		})

		// 2. Pub/Sub Listener check
		eg.Go(func() error {
			if listener == nil {
				return fmt.Errorf("pubsub listener is nil")
			}
			if !listener.IsHealthy() {
				return fmt.Errorf("pubsub listener is not running")
			}
			logging.Debug(ctx, "Pub/Sub listener check completed successfully")
			return nil
		})

		if err := eg.Wait(); err != nil {
			logging.Error(ctx, "Active health check failed", "error", err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}
}
