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
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func TestK8sHelmManager_ComponentHealth(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	releaseKey := componentKey(ref.Namespace, ComponentReleaseName(ref.Name))
	type health struct {
		Healthy bool
		Reason  string
	}
	tests := []struct {
		name          string
		ref           ComponentRef
		noRunner      bool
		installed     bool
		releaseStatus release.Status
		statusErr     error
		upgradeErr    error
		pending       bool
		missing       []string
		missingErr    error
		want          health
	}{
		{name: "deployed release is healthy", ref: ref, installed: true, want: health{true, ReasonComponentHealthy}},
		{name: "deployed release missing objects is drifted", ref: ref, installed: true, missing: []string{"Role/ingestion-gateway-components-reader"}, want: health{false, ReasonHelmReleaseDrifted}},
		{name: "resource lookup error trusts deployed status", ref: ref, installed: true, missingErr: errors.New("connection refused"), want: health{true, ReasonComponentHealthy}},
		{name: "last upgrade failed", ref: ref, installed: true, upgradeErr: errors.New("image pull failed"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "upgrade in flight over previously deployed revision", ref: ref, installed: true, pending: true, want: health{false, ReasonHelmUpgradePending}},
		{name: "upgrade error takes precedence over pending retry", ref: ref, installed: true, pending: true, upgradeErr: errors.New("boom"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "release not found", ref: ref, want: health{false, ReasonHelmReleaseNotFound}},
		{name: "wrapped not found error", ref: ref, statusErr: fmt.Errorf("helm status failed: %w", driver.ErrReleaseNotFound), want: health{false, ReasonHelmReleaseNotFound}},
		{name: "non-sentinel not found text is transient", ref: ref, statusErr: errors.New(`namespaces "xmc" not found`), want: health{false, ReasonHelmStatusUnavailable}},
		{name: "release stuck in pending-install", ref: ref, installed: true, releaseStatus: release.StatusPendingInstall, want: health{false, ReasonHelmReleaseStuck}},
		{name: "release stuck in pending-upgrade", ref: ref, installed: true, releaseStatus: release.StatusPendingUpgrade, want: health{false, ReasonHelmReleaseStuck}},
		{name: "release failed", ref: ref, installed: true, releaseStatus: release.StatusFailed, want: health{false, ReasonHelmReleaseNotReady}},
		{name: "status query error", ref: ref, statusErr: errors.New("connection refused"), want: health{false, ReasonHelmStatusUnavailable}},
		{name: "runner uninitialized", ref: ref, noRunner: true, want: health{false, ReasonHelmRunnerUninitialized}},
		{name: "upgrade error takes precedence over missing runner", ref: ref, noRunner: true, upgradeErr: errors.New("helm action runner is uninitialized"), want: health{false, ReasonHelmUpgradeFailed}},
		{name: "empty namespace", ref: ComponentRef{Name: "ingestion-gateway"}, want: health{false, ReasonInvalidComponentRef}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &mockHelmActionRunner{installed: map[string]bool{releaseKey: tt.installed}, releaseStatus: tt.releaseStatus, statusErr: tt.statusErr, missing: tt.missing, missingErr: tt.missingErr}
			opts := []HelmManagerOption{WithSyncExecution(true)}
			if !tt.noRunner {
				opts = append(opts, WithActionRunner(runner))
			}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", opts...)
			defer mgr.Close()
			mgr.setUpgradeErr(ref, tt.upgradeErr)
			if tt.pending {
				mgr.markUpgradePending(ref)
			}

			healthy, reason, msg := mgr.ComponentHealth(context.Background(), tt.ref)
			if diff := cmp.Diff(tt.want, health{healthy, reason}); diff != "" {
				t.Errorf("ComponentHealth() mismatch (-want +got):\n%s\nmessage: %s", diff, msg)
			}
			if msg == "" {
				t.Error("ComponentHealth() returned empty message")
			}
		})
	}

	var nilMgr *K8sHelmManager
	if healthy, reason, _ := nilMgr.ComponentHealth(context.Background(), ref); healthy || reason != ReasonHelmRunnerUninitialized {
		t.Errorf("nil manager ComponentHealth() = (%v, %q), want (false, %q)", healthy, reason, ReasonHelmRunnerUninitialized)
	}
}

// TestK8sHelmManager_ComponentHealth_CachesDeployedVerdict verifies Status is not queried on every poll while the
// release is deployed, that the cache expires after healthCacheTTL, and that release operations invalidate it.
func TestK8sHelmManager_ComponentHealth_CachesDeployedVerdict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
		runner := &mockHelmActionRunner{installed: map[string]bool{componentKey(ref.Namespace, ComponentReleaseName(ref.Name)): true}}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
		defer mgr.Close()
		statusCalls := func() int {
			runner.mu.Lock()
			defer runner.mu.Unlock()
			return runner.statusCalls
		}

		for range 3 {
			if healthy, reason, _ := mgr.ComponentHealth(ctx, ref); !healthy {
				t.Fatalf("ComponentHealth() unhealthy: %s", reason)
			}
		}
		if got := statusCalls(); got != 1 {
			t.Errorf("Status calls within TTL = %d, want 1", got)
		}

		time.Sleep(healthCacheTTL)
		mgr.ComponentHealth(ctx, ref)
		if got := statusCalls(); got != 2 {
			t.Errorf("Status calls after TTL expiry = %d, want 2", got)
		}

		mgr.setUpgradeErr(ref, nil) // Any release operation boundary invalidates the cache.
		mgr.ComponentHealth(ctx, ref)
		if got := statusCalls(); got != 3 {
			t.Errorf("Status calls after invalidation = %d, want 3", got)
		}
	})
}
