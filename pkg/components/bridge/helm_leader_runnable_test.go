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
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

// recordingLeaderHelm is a leaderHelmManager that records its calls.
type recordingLeaderHelm struct {
	hydrateErr  error
	shutdownErr error

	mu               sync.Mutex
	hydrations       int
	shutdowns        int
	shutdownDeadline time.Time
}

func (h *recordingLeaderHelm) HydrateFromCluster(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hydrations++
	return h.hydrateErr
}

func (h *recordingLeaderHelm) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shutdowns++
	h.shutdownDeadline, _ = ctx.Deadline()
	return h.shutdownErr
}

// calls returns the number of hydrations and shutdowns so far.
func (h *recordingLeaderHelm) calls() (hydrations, shutdowns int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hydrations, h.shutdowns
}

// TestNewHelmLeaderRunnable checks that the constructed runnable needs leader election and gives
// the Helm manager helmShutdownTimeout to finish its upgrades.
func TestNewHelmLeaderRunnable(t *testing.T) {
	helm := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud")
	defer helm.Close()
	r, ok := NewHelmLeaderRunnable(helm).(*helmLeaderRunnable)
	if !ok {
		t.Fatalf("NewHelmLeaderRunnable() returned %T, want *helmLeaderRunnable", NewHelmLeaderRunnable(helm))
	}
	if r.helm != helm || r.shutdownTimeout != helmShutdownTimeout || !r.NeedLeaderElection() {
		t.Errorf("NewHelmLeaderRunnable() = {helm: %p, shutdownTimeout: %v, leader: %t}, want {helm: %p, shutdownTimeout: %v, leader: true}", r.helm, r.shutdownTimeout, r.NeedLeaderElection(), helm, helmShutdownTimeout)
	}
}

// TestHelmLeaderRunnable checks that the runnable runs only on the leader, hydrates the Helm
// manager once started, even when hydration fails keeps running until the manager stops leading,
// and then shuts the Helm manager down with helmShutdownTimeout to finish its upgrades, returning
// nil even when the shutdown cancelled unfinished upgrades.
func TestHelmLeaderRunnable(t *testing.T) {
	tests := []struct {
		name        string
		hydrateErr  error
		shutdownErr error
	}{
		{name: "hydrated"},
		{name: "hydration failed", hydrateErr: errors.New("configmap unavailable")},
		{name: "shutdown cancelled upgrades", shutdownErr: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				checkHelmLeaderRunnableLifecycle(t, &recordingLeaderHelm{hydrateErr: tt.hydrateErr, shutdownErr: tt.shutdownErr})
			})
		})
	}
}

// checkHelmLeaderRunnableLifecycle starts a helmLeaderRunnable over helm, stops it, and checks its
// Helm manager calls. It must run inside a synctest bubble.
func checkHelmLeaderRunnableLifecycle(t *testing.T, helm *recordingLeaderHelm) {
	t.Helper()
	r := &helmLeaderRunnable{helm: helm, shutdownTimeout: helmShutdownTimeout}
	if !r.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = false, want true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	synctest.Wait()
	if hydrations, shutdowns := helm.calls(); hydrations != 1 || shutdowns != 0 {
		t.Errorf("while leading: hydrations = %d, shutdowns = %d; want 1, 0", hydrations, shutdowns)
	}

	cancel()
	stoppedAt := time.Now()
	if err := <-done; err != nil {
		t.Errorf("Start() = %v, want nil", err)
	}
	if _, shutdowns := helm.calls(); shutdowns != 1 {
		t.Errorf("shutdowns after stop = %d, want 1", shutdowns)
	}
	if want := stoppedAt.Add(helmShutdownTimeout); !helm.shutdownDeadline.Equal(want) {
		t.Errorf("Shutdown deadline = %v, want %v", helm.shutdownDeadline, want)
	}
}

// TestK8sHelmManager_ShutdownDropsQueuedUpgrades checks that Shutdown with time left waits for the
// in-flight upgrade to finish but never starts a queued one: the stepping-down leader records
// errManagerStopped for it and leaves it to the next leader.
func TestK8sHelmManager_ShutdownDropsQueuedUpgrades(t *testing.T) {
	inFlight := ComponentRef{Namespace: "xmc", Name: "in-flight"}
	queued := ComponentRef{Namespace: "xmc", Name: "queued"}
	gate := make(chan struct{})
	runner := &mockHelmActionRunner{upgradeGate: gate, upgradeStarted: make(chan struct{}, 1)}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithDebounceDuration(time.Millisecond))
	if err := mgr.Ensure1PComponent(context.Background(), inFlight, HelmComponentOptions{Ref: inFlight, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(%s) failed: %v", inFlight.Name, err)
	}
	<-runner.upgradeStarted
	if err := mgr.Ensure1PComponent(context.Background(), queued, HelmComponentOptions{Ref: queued, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(%s) failed: %v", queued.Name, err)
	}

	done := make(chan error, 1)
	go func() { done <- mgr.Shutdown(context.Background()) }()
	<-mgr.stopCh // Shutdown has begun; only now let the in-flight upgrade finish.
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}

	if got, want := runner.recordedOps(), []string{"upgrade"}; !slices.Equal(got, want) {
		t.Errorf("completed release operations = %v, want %v", got, want)
	}
	if err := mgr.GetUpgradeError(inFlight); err != nil {
		t.Errorf("GetUpgradeError(%s) = %v, want nil", inFlight.Name, err)
	}
	if err := mgr.GetUpgradeError(queued); !errors.Is(err, errManagerStopped) {
		t.Errorf("GetUpgradeError(%s) = %v, want %v", queued.Name, err, errManagerStopped)
	}
	if mgr.isUpgradePending(queued) {
		t.Errorf("isUpgradePending(%s) = true after Shutdown, want false", queued.Name)
	}
}

// TestK8sHelmManager_UpgradeWaitingOnLockIsDroppedOnShutdown checks that an async upgrade blocked
// on the upgrade lock behind a teardown when shutdown begins is dropped once it gets the lock: the
// manager's context is still live then, but the upgrade must not start after shutdown began.
func TestK8sHelmManager_UpgradeWaitingOnLockIsDroppedOnShutdown(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "queued"}
	runner := &mockHelmActionRunner{}
	// Sync execution starts no worker, so the test drives executeComponentUpgrade itself.
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer mgr.cancel()
	req := upgradeRequest{ref: ref, namespace: ref.Namespace, seq: mgr.markUpgradePending(ref)}
	if err := mgr.acquireUpgradeLock(context.Background()); err != nil { // Held by a teardown.
		t.Fatalf("acquireUpgradeLock() failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.executeComponentUpgrade(req)
	}()

	// Close's first phase: stopCh is closed while m.ctx stays live until the worker returns.
	mgr.stopMu.Lock()
	mgr.stopped = true
	close(mgr.stopCh)
	mgr.stopMu.Unlock()
	mgr.releaseUpgradeLock()
	<-done

	if got := runner.recordedOps(); len(got) != 0 {
		t.Errorf("completed release operations = %v, want none", got)
	}
	if err := mgr.GetUpgradeError(ref); !errors.Is(err, errManagerStopped) {
		t.Errorf("GetUpgradeError(%s) = %v, want %v", ref.Name, err, errManagerStopped)
	}
	if mgr.isUpgradePending(ref) {
		t.Errorf("isUpgradePending(%s) = true, want false", ref.Name)
	}
}

// TestK8sHelmManager_EnsureAfterStopRecordsManagerStopped checks that an upgrade requested after
// the manager stopped records errManagerStopped as the component's upgrade error, like a dropped
// queued upgrade, so a retry on the same manager does not take the unchanged-values shortcut.
func TestK8sHelmManager_EnsureAfterStopRecordsManagerStopped(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "late"}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(&mockHelmActionRunner{}), WithDebounceDuration(time.Millisecond))
	_ = mgr.Close()
	if err := mgr.Ensure1PComponent(context.Background(), ref, HelmComponentOptions{Ref: ref, Enabled: true}); !errors.Is(err, errManagerStopped) {
		t.Fatalf("Ensure1PComponent() after stop = %v, want %v", err, errManagerStopped)
	}
	if err := mgr.GetUpgradeError(ref); !errors.Is(err, errManagerStopped) {
		t.Errorf("GetUpgradeError(%s) = %v, want %v", ref.Name, err, errManagerStopped)
	}
}

// TestK8sHelmManager_StopCancelsUpgrades checks that Shutdown whose context ended, and Abort,
// cancel the in-flight upgrade and never start a queued one, so no Helm write outlives a
// leader's shutdown, and that a second Abort, as after a graceful shutdown, returns at once.
func TestK8sHelmManager_StopCancelsUpgrades(t *testing.T) {
	tests := []struct {
		name string
		stop func(t *testing.T, mgr *K8sHelmManager)
	}{
		{
			name: "Shutdown with ended context",
			stop: func(t *testing.T, mgr *K8sHelmManager) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := mgr.Shutdown(ctx); !errors.Is(err, context.Canceled) {
					t.Errorf("Shutdown() = %v, want %v", err, context.Canceled)
				}
			},
		},
		{
			name: "Abort",
			stop: func(t *testing.T, mgr *K8sHelmManager) {
				mgr.Abort()
				mgr.Abort()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { checkStopCancelsUpgrades(t, tt.stop) })
	}
}

// checkStopCancelsUpgrades stops a manager with one blocked in-flight and one queued upgrade
// through stop, and checks that the in-flight upgrade was cancelled and the queued one dropped.
func checkStopCancelsUpgrades(t *testing.T, stop func(t *testing.T, mgr *K8sHelmManager)) {
	t.Helper()
	inFlight := ComponentRef{Namespace: "xmc", Name: "in-flight"}
	queued := ComponentRef{Namespace: "xmc", Name: "queued"}
	// The gate is never opened: the in-flight upgrade returns only when its context is cancelled.
	runner := &mockHelmActionRunner{upgradeGate: make(chan struct{}), upgradeStarted: make(chan struct{}, 1)}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithDebounceDuration(time.Millisecond))
	if err := mgr.Ensure1PComponent(context.Background(), inFlight, HelmComponentOptions{Ref: inFlight, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(%s) failed: %v", inFlight.Name, err)
	}
	<-runner.upgradeStarted
	if err := mgr.Ensure1PComponent(context.Background(), queued, HelmComponentOptions{Ref: queued, Enabled: true}); err != nil {
		t.Fatalf("Ensure1PComponent(%s) failed: %v", queued.Name, err)
	}

	stop(t, mgr)
	if err := mgr.GetUpgradeError(inFlight); !errors.Is(err, context.Canceled) {
		t.Errorf("GetUpgradeError(%s) = %v, want %v", inFlight.Name, err, context.Canceled)
	}
	if err := mgr.GetUpgradeError(queued); !errors.Is(err, errManagerStopped) {
		t.Errorf("GetUpgradeError(%s) = %v, want %v", queued.Name, err, errManagerStopped)
	}
	if got := runner.recordedOps(); len(got) != 0 {
		t.Errorf("completed release operations = %v, want none", got)
	}
}
