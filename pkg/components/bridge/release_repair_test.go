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
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/release"
)

// TestK8sHelmManager_ReleaseRepair verifies that an actuated component whose release drifts, disappears, or fails is
// re-upgraded at the same generation by the next Ensure1PComponent, while transient errors never trigger a re-upgrade.
func TestK8sHelmManager_ReleaseRepair(t *testing.T) {
	const generation = 3
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	releaseKey := componentKey(ref.Namespace, ComponentReleaseName(ref.Name))
	type result struct {
		Reason      string
		NeedsRepair bool
		Reupgraded  bool
		FinalReason string
	}
	tests := []struct {
		name   string
		mutate func(r *mockHelmActionRunner)
		want   result
	}{
		{
			name:   "drifted release is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.missing = []string{"Role/ingestion-gateway-components-reader"} },
			want:   result{Reason: ReasonHelmReleaseDrifted, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "release deleted after actuation is re-installed",
			mutate: func(r *mockHelmActionRunner) { delete(r.installed, releaseKey) },
			want:   result{Reason: ReasonHelmReleaseNotFound, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "terminally failed release is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusFailed },
			want:   result{Reason: ReasonHelmReleaseNotReady, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "transient status error keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.statusErr = errors.New("connection refused") },
			want:   result{Reason: ReasonHelmStatusUnavailable, FinalReason: ReasonHelmStatusUnavailable},
		},
		{
			name:   "transient resource lookup error keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.missingErr = errors.New("connection refused") },
			want:   result{Reason: ReasonComponentHealthy, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "stuck pending release keeps actuation",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusPendingUpgrade },
			want:   result{Reason: ReasonHelmReleaseStuck, FinalReason: ReasonHelmReleaseStuck},
		},
		{
			name:   "release uninstalled with kept history is re-upgraded and recovers",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusUninstalled },
			want:   result{Reason: ReasonHelmReleaseNotReady, NeedsRepair: true, Reupgraded: true, FinalReason: ReasonComponentHealthy},
		},
		{
			name:   "release being uninstalled is not repaired",
			mutate: func(r *mockHelmActionRunner) { r.releaseStatus = release.StatusUninstalling },
			want:   result{Reason: ReasonHelmReleaseNotReady, FinalReason: ReasonHelmReleaseNotReady},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			runner := &mockHelmActionRunner{}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr.Close()
			if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("initial Ensure1PComponent failed: %v", err)
			}
			mgr.RecordActuation(ref, generation)
			runner.mu.Lock()
			tt.mutate(runner)
			runner.mu.Unlock()

			var got result
			_, got.Reason, _ = mgr.ComponentHealth(ctx, ref)
			got.NeedsRepair = mgr.NeedsRepair(ref, generation)
			if !mgr.IsComponentActuated(ref, generation) {
				t.Error("IsComponentActuated() = false after health check, want true (repair is reported by NeedsRepair)")
			}
			if mgr.NeedsRepair(ref, generation+1) {
				t.Error("NeedsRepair() = true for a generation that was never actuated, want false")
			}
			if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("repair Ensure1PComponent failed: %v", err)
			}
			mgr.RecordActuation(ref, generation)
			runner.mu.Lock()
			got.Reupgraded = runner.upgradeCalls > 1
			runner.mu.Unlock()
			_, got.FinalReason, _ = mgr.ComponentHealth(ctx, ref)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("release repair mismatch (-want +got):\n%s", diff)
			}
			if mgr.NeedsRepair(ref, generation) {
				t.Error("NeedsRepair() = true after repair upgrade, want false")
			}
		})
	}
}

// TestReleaseNeedsRepair verifies that only missing, drifted, failed, or uninstalled releases trigger repair, and that
// in-flight states such as uninstalling never do.
func TestReleaseNeedsRepair(t *testing.T) {
	tests := []struct {
		reason string
		status release.Status
		want   bool
	}{
		{reason: ReasonHelmReleaseNotFound, status: release.StatusUnknown, want: true},
		{reason: ReasonHelmReleaseDrifted, status: release.StatusDeployed, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusFailed, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUninstalled, want: true},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUninstalling, want: false},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusUnknown, want: false},
		{reason: ReasonHelmReleaseNotReady, status: release.StatusSuperseded, want: false},
		{reason: ReasonHelmReleaseStuck, status: release.StatusPendingUpgrade, want: false},
		{reason: ReasonHelmStatusUnavailable, status: release.StatusUnknown, want: false},
		{reason: ReasonComponentHealthy, status: release.StatusDeployed, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.reason+"/"+tt.status.String(), func(t *testing.T) {
			if got := releaseNeedsRepair(tt.reason, tt.status); got != tt.want {
				t.Errorf("releaseNeedsRepair(%q, %q) = %v, want %v", tt.reason, tt.status, got, tt.want)
			}
		})
	}
}

// TestK8sHelmManager_RepairBackoff verifies that a release whose objects keep being deleted is not re-upgraded on
// every health sweep: repairs back off exponentially, and the backoff resets on a new generation or after the release
// stays healthy for repairBackoffMax.
func TestK8sHelmManager_RepairBackoff(t *testing.T) {
	const generation = 3
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	key := componentKey(ref.Namespace, ref.Name)
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	deleted := []string{"Role/ingestion-gateway-components-reader"}

	setup := func(t *testing.T) (*K8sHelmManager, *mockHelmActionRunner) {
		t.Helper()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
		t.Cleanup(func() { mgr.Close() })
		mgr.jitter = func() float64 { return 0 }
		if err := mgr.Ensure1PComponent(context.Background(), ref, opts); err != nil {
			t.Fatalf("initial Ensure1PComponent failed: %v", err)
		}
		mgr.RecordActuation(ref, generation)
		return mgr, runner
	}
	// sweep simulates an actor that deletes an object before each health check, then runs one reconcile pass and
	// reports whether it re-upgraded the release.
	sweep := func(t *testing.T, mgr *K8sHelmManager, runner *mockHelmActionRunner) bool {
		t.Helper()
		ctx := context.Background()
		runner.mu.Lock()
		runner.missing = deleted
		before := runner.upgradeCalls
		runner.mu.Unlock()
		if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != ReasonHelmReleaseDrifted {
			t.Fatalf("ComponentHealth() reason = %q, want %q", reason, ReasonHelmReleaseDrifted)
		}
		if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
			t.Fatalf("Ensure1PComponent failed: %v", err)
		}
		mgr.RecordActuation(ref, generation)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.upgradeCalls > before
	}
	attempts := func(mgr *K8sHelmManager) int {
		mgr.mu.RLock()
		defer mgr.mu.RUnlock()
		return mgr.repairBackoff[key].count
	}

	t.Run("backs off exponentially between repairs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mgr, runner := setup(t)
			// Each step waits, then sweeps; the first repair is immediate, later ones wait 30s, then 60s.
			steps := []struct {
				wait time.Duration
				want bool
			}{
				{0, true},
				{0, false},
				{29 * time.Second, false},
				{time.Second, true},
				{30 * time.Second, false},
				{30 * time.Second, true},
			}
			for i, s := range steps {
				time.Sleep(s.wait)
				if got := sweep(t, mgr, runner); got != s.want {
					t.Errorf("step %d (after %v): re-upgraded = %v, want %v", i, s.wait, got, s.want)
				}
			}
			if got := attempts(mgr); got != 3 {
				t.Errorf("repair attempts = %d, want 3", got)
			}
		})
	})

	t.Run("new generation resets backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mgr, runner := setup(t)
			sweep(t, mgr, runner)
			mgr.RecordActuation(ref, generation+1)
			if got := attempts(mgr); got != 0 {
				t.Errorf("repair attempts after new generation = %d, want 0", got)
			}
		})
	})

	t.Run("only a sustained healthy period resets backoff", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			mgr, runner := setup(t)
			sweep(t, mgr, runner)
			runner.mu.Lock()
			runner.missing = nil
			runner.mu.Unlock()
			for _, tc := range []struct {
				wait time.Duration
				want int
			}{
				{healthCacheTTL, 1},
				{repairBackoffMax, 0},
			} {
				time.Sleep(tc.wait)
				if healthy, reason, _ := mgr.ComponentHealth(ctx, ref); !healthy {
					t.Fatalf("ComponentHealth() unhealthy: %s", reason)
				}
				if got := attempts(mgr); got != tc.want {
					t.Errorf("repair attempts after healthy check at +%v = %d, want %d", tc.wait, got, tc.want)
				}
			}
		})
	})
}

// TestK8sHelmManager_RepairDelay verifies the repair backoff schedule doubles from repairBackoffBase, caps at
// repairBackoffMax, and adds at most repairBackoffJitter of randomized delay.
func TestK8sHelmManager_RepairDelay(t *testing.T) {
	tests := []struct {
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{attempt: 1, want: 30 * time.Second},
		{attempt: 2, want: time.Minute},
		{attempt: 3, want: 2 * time.Minute},
		{attempt: 5, want: 8 * time.Minute},
		{attempt: 6, want: 10 * time.Minute},
		{attempt: 100, want: 10 * time.Minute},
		{attempt: 1, jitter: 0.5, want: 31500 * time.Millisecond},
		{attempt: 100, jitter: 0.999, want: 10*time.Minute + time.Duration(float64(10*time.Minute)*0.1*0.999)},
	}
	for _, tt := range tests {
		mgr := &K8sHelmManager{jitter: func() float64 { return tt.jitter }}
		if got := mgr.repairDelay(tt.attempt); got != tt.want {
			t.Errorf("repairDelay(%d) with jitter %v = %v, want %v", tt.attempt, tt.jitter, got, tt.want)
		}
	}
}

// TestK8sHelmManager_TeardownClearsRepair verifies that uninstalling, purging, or orphaning a component drops any
// pending repair marker.
func TestK8sHelmManager_TeardownClearsRepair(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	tests := []struct {
		name     string
		teardown func(context.Context, *K8sHelmManager) error
	}{
		{name: "Delete1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Delete1PComponent(ctx, ref) }},
		{name: "Purge1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Purge1PComponent(ctx, ref) }},
		{name: "Orphan1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Orphan1PComponent(ctx, ref) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			runner := &mockHelmActionRunner{}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr.Close()
			if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			runner.mu.Lock()
			runner.missing = []string{"Service/ingestion-gateway-service"}
			runner.mu.Unlock()
			if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != ReasonHelmReleaseDrifted {
				t.Fatalf("ComponentHealth() reason = %q, want %q", reason, ReasonHelmReleaseDrifted)
			}
			key := componentKey(ref.Namespace, ref.Name)
			mgr.mu.Lock()
			mgr.repairBackoff[key] = repairAttempt{count: 1}
			mgr.mu.Unlock()
			if err := tt.teardown(ctx, mgr); err != nil {
				t.Fatalf("%s failed: %v", tt.name, err)
			}
			mgr.mu.RLock()
			defer mgr.mu.RUnlock()
			if mgr.repairNeeded[key] {
				t.Errorf("repairNeeded still set after %s", tt.name)
			}
			if _, ok := mgr.repairBackoff[key]; ok {
				t.Errorf("repairBackoff still set after %s", tt.name)
			}
		})
	}
}
