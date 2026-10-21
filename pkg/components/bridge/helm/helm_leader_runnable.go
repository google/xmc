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

package helm

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"xmanager_cloud/internal/logging"
)

// HelmShutdownTimeout bounds how long a leader that steps down lets its in-flight Helm upgrade finish before it
// cancels it; queued upgrades that have not started are dropped at once, and the next leader re-reconciles them. It
// is shorter than componentGracefulShutdownTimeout, so the Helm worker normally returns before the manager releases
// the Lease. A cancelled Helm action can return while Helm still applies resources in the background, so Helm writes
// are bounded by the process exit that follows, not by the Lease release.
const HelmShutdownTimeout = 20 * time.Second

const helmShutdownTimeout = HelmShutdownTimeout

// leaderHelmManager is the part of K8sHelmManager whose work must stay within the Lease.
type leaderHelmManager interface {
	HydrateFromCluster(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// helmLeaderRunnable ties a Helm manager to the Lease: it hydrates the manager's state once elected, and stops the
// manager's Helm upgrades when the manager stops leading, before the Lease is released.
type helmLeaderRunnable struct {
	helm            leaderHelmManager
	shutdownTimeout time.Duration
}

var _ manager.LeaderElectionRunnable = (*helmLeaderRunnable)(nil)

// NewHelmLeaderRunnable returns a manager.Runnable that hydrates helm from the cluster once the manager is elected
// and shuts helm down when the manager stops leading. Queued Helm upgrades that have not started are dropped; the
// in-flight one gets helmShutdownTimeout to finish and is cancelled after it.
func NewHelmLeaderRunnable(helm *K8sHelmManager) manager.Runnable {
	return &helmLeaderRunnable{helm: helm, shutdownTimeout: helmShutdownTimeout}
}

// NeedLeaderElection makes the manager start the runnable only once it holds the Lease.
func (r *helmLeaderRunnable) NeedLeaderElection() bool { return true }

// Start hydrates the Helm manager, waits until ctx ends, and then shuts the Helm manager down.
func (r *helmLeaderRunnable) Start(ctx context.Context) error {
	if err := r.helm.HydrateFromCluster(ctx); err != nil {
		logging.Warn(ctx, "Failed to hydrate HelmManager state from cluster ConfigMap; components hydrate on their first reconcile", "error", err)
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.shutdownTimeout)
	defer cancel()
	if err := r.helm.Shutdown(shutdownCtx); err != nil {
		logging.Warn(ctx, "Cancelled Helm upgrades that did not finish before shutdown", "timeout", r.shutdownTimeout, "error", err)
	}
	return nil
}
