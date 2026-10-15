// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// ComponentLeaseName names the Lease, in the reconciled namespace, whose holder is the only
// xmc-bridge that reconciles the namespace's Components.
const ComponentLeaseName = "xmc-bridge"

// cacheSyncCheckTimeout bounds how long a readiness check waits for the informer cache.
const cacheSyncCheckTimeout = time.Second

// componentGracefulShutdownTimeout bounds how long a stopping manager waits for its controllers
// and the Helm runnable before it releases the Lease. The Helm Deployment's
// terminationGracePeriodSeconds must exceed it.
const componentGracefulShutdownTimeout = 25 * time.Second

// ComponentManagerConfig configures the controller-runtime manager of an xmc-bridge.
type ComponentManagerConfig struct {
	// Namespace is the namespace whose Components the manager caches and reconciles.
	Namespace string
	// LeaderElection makes the manager run its controllers only while it holds the
	// ComponentLeaseName Lease in Namespace, so that a single xmc-bridge writes to the
	// namespace's Components, CMS records and releases.
	LeaderElection bool
	// MetricsBindAddress is the address of the metrics endpoint. "" or "0" disables it.
	MetricsBindAddress string
	// HealthProbeBindAddress is the address of the /healthz and /readyz endpoints. "" or "0"
	// disables them.
	HealthProbeBindAddress string
}

// ComponentManagerOptions returns the manager options for cfg. The cache is limited to
// cfg.Namespace, and the manager's client serves unstructured Gets and Lists from that cache
// instead of the API server. A leader that is stopped waits up to
// componentGracefulShutdownTimeout for its runnables and then releases the Lease, so that its
// successor takes over without waiting for the Lease to expire; the process must exit once the
// manager stops.
func ComponentManagerOptions(cfg ComponentManagerConfig) manager.Options {
	metricsAddr := cfg.MetricsBindAddress
	if metricsAddr == "" {
		metricsAddr = "0"
	}
	gracefulShutdownTimeout := componentGracefulShutdownTimeout
	return manager.Options{
		Cache:                         cache.Options{DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}}},
		Client:                        client.Options{Cache: &client.CacheOptions{Unstructured: true}},
		Metrics:                       metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:        cfg.HealthProbeBindAddress,
		LeaderElection:                cfg.LeaderElection,
		LeaderElectionID:              ComponentLeaseName,
		LeaderElectionNamespace:       cfg.Namespace,
		LeaderElectionReleaseOnCancel: true,
		GracefulShutdownTimeout:       &gracefulShutdownTimeout,
	}
}

// AddComponentHealthChecks registers the liveness check, which only reports that the process
// serves requests, and the readiness check, which passes once the Component cache has synced.
// It registers the Component informer up front: a follower runs no controller that would
// register it, so without it the follower's cache would hold no informer and report synced at
// once. Followers therefore sync the Components too and are ready while they wait for the Lease.
func AddComponentHealthChecks(ctx context.Context, mgr manager.Manager) error {
	if err := registerComponentInformer(ctx, mgr.GetCache()); err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("failed to add liveness check: %w", err)
	}
	if err := mgr.AddReadyzCheck("cache-sync", cacheSyncedChecker(mgr.GetCache())); err != nil {
		return fmt.Errorf("failed to add readiness check: %w", err)
	}
	return nil
}

// registerComponentInformer adds the Component informer to c without waiting for it to sync, so
// that c starts it with the manager.
func registerComponentInformer(ctx context.Context, c cache.Informers) error {
	if _, err := c.GetInformer(ctx, newComponentObject(), cache.BlockUntilSynced(false)); err != nil {
		return fmt.Errorf("failed to register the Component informer: %w", err)
	}
	return nil
}

// cacheSyncedChecker passes once every informer of c, including the Component informer, has
// synced.
func cacheSyncedChecker(c cache.Cache) healthz.Checker {
	return func(req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), cacheSyncCheckTimeout)
		defer cancel()
		if !c.WaitForCacheSync(ctx) {
			return errors.New("component cache has not synced")
		}
		return nil
	}
}
