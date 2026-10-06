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
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func TestK8sHelmManager_SyncUpgrade(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
		WithChartPath("deploy/helm"),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	opts := HelmComponentOptions{
		Ref:      ref,
		Enabled:  true,
		ImageTag: "v1.2.3",
	}

	// 1. Ensure component triggers synchronous upgrade with per-component release name
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	expectedRelease := ComponentReleaseName("rod-data-provider")
	runner.mu.Lock()
	if runner.upgradeCalls != 1 {
		t.Errorf("expected 1 upgrade call, got %d", runner.upgradeCalls)
	}
	if runner.lastRelease != expectedRelease {
		t.Errorf("expected release %q, got %q", expectedRelease, runner.lastRelease)
	}
	if runner.lastNS != "xmc" {
		t.Errorf("expected namespace xmc, got %q", runner.lastNS)
	}
	compMap, ok := runner.lastValues["rodDeployment"].(map[string]any)
	enabled, _ := compMap["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("expected rodDeployment.enabled=true in upgrade values, got %v", runner.lastValues)
	}
	runner.mu.Unlock()

	// 2. Delete component triggers uninstallation of component release
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	runner.mu.Lock()
	if runner.uninstallCalls != 1 {
		t.Errorf("expected 1 uninstall call, got %d", runner.uninstallCalls)
	}
	if runner.lastUninstall != expectedRelease {
		t.Errorf("expected uninstall release %q, got %q", expectedRelease, runner.lastUninstall)
	}
	runner.mu.Unlock()

	// 3. Error propagation during upgrade
	runner.mu.Lock()
	runner.upgradeErr = errors.New("simulated helm failure")
	runner.mu.Unlock()

	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil || !errors.Is(err, runner.upgradeErr) {
		t.Errorf("expected simulated helm failure error, got %v", err)
	}
}

func TestK8sHelmManager_AsyncUpgrade(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "health-service"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent async failed: %v", err)
	}

	// Wait up to 1 second for background worker execution
	var calls int
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		runner.mu.Lock()
		calls = runner.upgradeCalls
		runner.mu.Unlock()
		if calls > 0 {
			break
		}
	}
	if calls == 0 {
		t.Error("expected background worker to execute upgrade")
	}

	if err := mgr.GetUpgradeError(ref); err != nil {
		t.Errorf("expected nil upgrade error on success, got %v", err)
	}
}

func TestK8sHelmManager_AsyncUpgrade_Failure(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{
		upgradeErr: errors.New("async upgrade network error"),
	}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "health-service"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("unexpected synchronous error on async Ensure1PComponent: %v", err)
	}

	// Wait up to 1 second for background worker failure to record
	var recordedErr error
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		recordedErr = mgr.GetUpgradeError(ref)
		if recordedErr != nil {
			break
		}
	}
	if recordedErr == nil || !errors.Is(recordedErr, runner.upgradeErr) {
		t.Errorf("expected recorded async error %v, got %v", runner.upgradeErr, recordedErr)
	}
}

func TestK8sHelmManager_Close(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
	)

	// Idempotent Close
	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}

	// Triggering upgrade after close should return error
	ref := ComponentRef{Namespace: "xmc", Name: "test-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	err := mgr.Ensure1PComponent(context.Background(), ref, opts)
	if err == nil || !strings.Contains(err.Error(), "helm manager is stopped") {
		t.Errorf("expected stopped manager error, got %v", err)
	}
}

func TestK8sHelmManager_CloseDrainsPendingUpgrades(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(100*time.Millisecond),
	)

	ref := ComponentRef{Namespace: "xmc", Name: "drained-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	// Immediate Close should drain pending upgrades without context.Canceled error
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := mgr.GetUpgradeError(ref); err != nil {
		t.Errorf("expected no upgrade error after drainage, got %v", err)
	}
	if runner.upgradeCalls != 1 {
		t.Errorf("expected 1 upgrade call after drainage, got %d", runner.upgradeCalls)
	}
}

func TestK8sHelmManager_DebouncedBatchUpgrade(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		client := fake.NewClientset()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
			WithActionRunner(runner),
			WithSyncExecution(false),
			WithDebounceDuration(30*time.Millisecond),
		)
		defer mgr.Close()

		refA := ComponentRef{Namespace: "xmc", Name: "component-a"}
		optsA1 := HelmComponentOptions{Ref: refA, Enabled: true, ImageTag: "v1"}
		optsA2 := HelmComponentOptions{Ref: refA, Enabled: true, ImageTag: "v2"}

		// Trigger rapid updates for component A within 30ms window
		if err := mgr.Ensure1PComponent(ctx, refA, optsA1); err != nil {
			t.Fatalf("Ensure1PComponent A1 failed: %v", err)
		}
		if err := mgr.Ensure1PComponent(ctx, refA, optsA2); err != nil {
			t.Fatalf("Ensure1PComponent A2 failed: %v", err)
		}

		// Advance virtual time past the debounce duration
		time.Sleep(100 * time.Millisecond)

		runner.mu.Lock()
		calls := runner.upgradeCalls
		runner.mu.Unlock()

		if calls != 1 {
			t.Errorf("expected 1 debounced upgrade call for component-a, got %d", calls)
		}
		if err := mgr.GetUpgradeError(refA); err != nil {
			t.Errorf("expected nil error for refA, got %v", err)
		}
	})
}

type panicHelmActionRunner struct {
	mockHelmActionRunner
}

func (p *panicHelmActionRunner) Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error) {
	panic("simulated upgrade panic")
}

func TestK8sHelmManager_ExecuteBatch_UpgradePanicUnlock(t *testing.T) {
	client := fake.NewClientset()
	runner := &panicHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()
	ref := ComponentRef{Namespace: "xmc", Name: "panic-comp"}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from panicHelmActionRunner")
		}
		if held := len(mgr.upgradeSem); held != 0 {
			t.Errorf("upgrade lock held after panic: len(upgradeSem) = %d, want 0", held)
		}
	}()

	mgr.executeComponentUpgrade(upgradeRequest{
		ref:       ref,
		namespace: "xmc",
		values:    map[string]any{"key": "val"},
		seq:       mgr.markUpgradePending(ref),
	})
}

func TestK8sHelmManager_Close_SynchronizesWithTriggerUpgrade(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(10*time.Millisecond),
	)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch concurrent triggerUpgrade calls
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := ComponentRef{Namespace: "xmc", Name: "comp"}
			_ = mgr.triggerUpgrade(ctx, ref, map[string]any{"val": "test"})
		}()
	}

	// Close concurrently
	_ = mgr.Close()
	wg.Wait()

	// Any subsequent triggerUpgrade must return stopped error
	ref := ComponentRef{Namespace: "xmc", Name: "comp-after-close"}
	err := mgr.triggerUpgrade(ctx, ref, map[string]any{"val": 999})
	if err == nil {
		t.Error("expected error from triggerUpgrade after Close, got nil")
	}
}

func TestK8sHelmManager_TriggerUpgrade_ChannelSaturation_NoDeadlock(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(5*time.Millisecond),
	)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Launch 150 concurrent triggerUpgrade calls exceeding channel buffer size (100)
	var wg sync.WaitGroup
	for i := 0; i < 150; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := ComponentRef{Namespace: "xmc", Name: "comp"}
			_ = mgr.triggerUpgrade(ctx, ref, map[string]any{"val": "saturated"})
		}()
	}

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Success, no deadlock occurred
	case <-ctx.Done():
		t.Fatal("deadlock detected: triggerUpgrade calls did not complete within timeout")
	}
}

func TestK8sHelmManager_DrainRemainingUpgrades_ClosedChannel(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	// Explicitly close upgradeChan
	close(mgr.upgradeChan)

	// drainRemainingUpgrades must safely terminate and not loop indefinitely
	doneCh := make(chan struct{})
	go func() {
		mgr.drainRemainingUpgrades()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Success, terminated cleanly on closed channel
	case <-time.After(1 * time.Second):
		t.Fatal("drainRemainingUpgrades hung indefinitely on closed channel")
	}
}

func TestK8sHelmManager_SyncExecution_StoppedManagerReturnsError(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	ref := ComponentRef{Namespace: "xmc", Name: "sync-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	err := mgr.Ensure1PComponent(context.Background(), ref, opts)
	if err == nil || !strings.Contains(err.Error(), "helm manager is stopped") {
		t.Errorf("expected stopped manager error, got %v", err)
	}
}

func TestK8sHelmManager_AsyncUpgrade_DoesNotClearErrorOnEnqueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		client := fake.NewClientset()
		runner := &mockHelmActionRunner{}
		mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
			WithActionRunner(runner),
			WithSyncExecution(false),
			WithDebounceDuration(30*time.Millisecond),
		)
		defer mgr.Close()

		ref := ComponentRef{Namespace: "xmc", Name: "test-comp"}
		expectedErr := errors.New("previous upgrade failed")
		mgr.setUpgradeErr(ref, expectedErr)

		opts := HelmComponentOptions{Ref: ref, Enabled: true}
		if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
			t.Fatalf("Ensure1PComponent failed: %v", err)
		}

		// Verify error is NOT cleared on enqueue
		if gotErr := mgr.GetUpgradeError(ref); gotErr == nil || gotErr.Error() != expectedErr.Error() {
			t.Fatalf("expected error to remain %v immediately after enqueue, got %v", expectedErr, gotErr)
		}

		// Advance virtual time so worker processes batch and succeeds
		time.Sleep(100 * time.Millisecond)

		// Error should now be cleared by the successful upgrade
		if gotErr := mgr.GetUpgradeError(ref); gotErr != nil {
			t.Errorf("expected error cleared after worker execution, got %v", gotErr)
		}
	})
}

func TestK8sHelmManager_WorkerRecoversPanic(t *testing.T) {
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{
		panicOnUpgrade: true,
	}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(false),
		WithDebounceDuration(10*time.Millisecond),
	)

	ref := ComponentRef{Namespace: "xmc", Name: "panicking-service"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}
	if err := mgr.Ensure1PComponent(context.Background(), ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	// Close waits for worker to exit via wg.Wait(). If panic wasn't recovered, Close or test would crash.
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_UninitializedRunner_ReturnsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}

	err := mgr.Ensure1PComponent(ctx, ref, opts)
	if err == nil {
		t.Fatal("expected error on Ensure1PComponent with uninitialized runner, got nil")
	}
	if !strings.Contains(err.Error(), "helm action runner is uninitialized") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_EmptyReleaseName_ReturnsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner))

	ref := ComponentRef{Namespace: "xmc", Name: ""}
	err := mgr.triggerUpgrade(ctx, ref, nil)
	if err == nil {
		t.Fatal("expected error on triggerUpgrade with empty component name, got nil")
	}
	if !strings.Contains(err.Error(), "helm release name must not be empty") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestK8sHelmManager_TriggerUpgrade_ContextCancellation_DuringLockAcquisition(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))

	if err := mgr.acquireUpgradeLock(ctx); err != nil {
		t.Fatalf("failed to acquire initial upgrade lock: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	err := mgr.triggerUpgrade(cancelCtx, ref, map[string]any{"foo": "bar"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled when acquiring lock with canceled context, got: %v", err)
	}

	mgr.releaseUpgradeLock()

	if err := mgr.triggerUpgrade(ctx, ref, map[string]any{"foo": "bar"}); err != nil {
		t.Fatalf("expected upgrade to succeed after releasing lock, got: %v", err)
	}
}

type panickingHelmActionRunner struct {
	mockHelmActionRunner
	panicOnUninstall bool
}

func (p *panickingHelmActionRunner) Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error) {
	if p.panicOnUninstall {
		panic("simulated uninstall panic")
	}
	return p.mockHelmActionRunner.Uninstall(ctx, releaseName, namespace)
}

func TestK8sHelmManager_UninstallRelease_NilRunner(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Manager without runner
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud")

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}
	// Delete1PComponent and Purge1PComponent should skip uninstallation safely without error
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Delete1PComponent with nil runner to succeed, got %v", err)
	}
	if err := mgr.Purge1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Purge1PComponent with nil runner to succeed, got %v", err)
	}
}

func TestK8sHelmManager_UninstallRelease_PanicSafeLockDefer(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	panickingRunner := &panickingHelmActionRunner{panicOnUninstall: true}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(panickingRunner))

	ref := ComponentRef{Namespace: "xmc", Name: "rod-data-provider"}

	// Verify that panic in runner.Uninstall is caught and lock is still released
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from panicking runner, got nil")
			}
		}()
		_ = mgr.Delete1PComponent(ctx, ref)
	}()

	// Since defer m.releaseUpgradeLock() ran during panic unwind, subsequent lock acquisition must not hang
	panickingRunner.panicOnUninstall = false
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("expected Delete1PComponent to succeed after recovered panic, got: %v", err)
	}
}

// TestK8sHelmManager_AsyncUpgrade_PendingUntilCompletion verifies the in-flight marker is set on enqueue and cleared
// only after the worker records the outcome, for both success and failure.
func TestK8sHelmManager_AsyncUpgrade_PendingUntilCompletion(t *testing.T) {
	tests := []struct {
		name       string
		upgradeErr error
		wantReason string
	}{
		{name: "success", wantReason: ReasonComponentHealthy},
		{name: "failure", upgradeErr: errors.New("install failed"), wantReason: ReasonHelmUpgradeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
			gate := make(chan struct{})
			runner := &mockHelmActionRunner{upgradeErr: tt.upgradeErr, upgradeGate: gate}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner))

			if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != ReasonHelmUpgradePending {
				t.Errorf("ComponentHealth() reason while in flight = %q, want %q", reason, ReasonHelmUpgradePending)
			}
			close(gate)
			_ = mgr.Close() // Waits for the worker to finish the upgrade.
			if _, reason, _ := mgr.ComponentHealth(ctx, ref); reason != tt.wantReason {
				t.Errorf("ComponentHealth() reason after completion = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestK8sHelmManager_TeardownWaitsForInFlightUpgrade verifies that Delete1PComponent never uninstalls a release while
// an asynchronous upgrade of it is still running: the uninstall waits for the upgrade to finish.
func TestK8sHelmManager_TeardownWaitsForInFlightUpgrade(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
		gate := make(chan struct{})
		runner := &mockHelmActionRunner{upgradeGate: gate, upgradeStarted: make(chan struct{}, 1)}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithDebounceDuration(time.Millisecond))
		defer mgr.Close()
		if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
			t.Fatalf("Ensure1PComponent failed: %v", err)
		}
		<-runner.upgradeStarted

		deleteErr := make(chan error, 1)
		go func() { deleteErr <- mgr.Delete1PComponent(ctx, ref) }()
		synctest.Wait()
		if got := runner.recordedOps(); len(got) != 0 {
			t.Errorf("release operations while the upgrade is in flight = %v, want none", got)
		}

		close(gate)
		if err := <-deleteErr; err != nil {
			t.Fatalf("Delete1PComponent failed: %v", err)
		}
		if diff := cmp.Diff([]string{"upgrade", "uninstall"}, runner.recordedOps()); diff != "" {
			t.Errorf("release operations mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestK8sHelmManager_TeardownDiscardsQueuedUpgrade verifies that tearing a component down discards its queued
// asynchronous upgrade, so the upgrade cannot recreate a release that was just uninstalled or orphaned.
func TestK8sHelmManager_TeardownDiscardsQueuedUpgrade(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	tests := []struct {
		name     string
		teardown func(context.Context, *K8sHelmManager) error
		wantOps  []string
	}{
		{name: "Delete1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Delete1PComponent(ctx, ref) }, wantOps: []string{"uninstall"}},
		{name: "Purge1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Purge1PComponent(ctx, ref) }, wantOps: []string{"uninstall"}},
		{name: "Orphan1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Orphan1PComponent(ctx, ref) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				runner := &mockHelmActionRunner{}
				mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithDebounceDuration(100*time.Millisecond))
				defer mgr.Close()
				if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true}); err != nil {
					t.Fatalf("Ensure1PComponent failed: %v", err)
				}
				if err := tt.teardown(ctx, mgr); err != nil {
					t.Fatalf("%s failed: %v", tt.name, err)
				}
				time.Sleep(200 * time.Millisecond) // Let the debounce window elapse so the worker processes the queued upgrade.
				synctest.Wait()

				if diff := cmp.Diff(tt.wantOps, runner.recordedOps()); diff != "" {
					t.Errorf("release operations mismatch (-want +got):\n%s", diff)
				}
				if mgr.isUpgradePending(ref) {
					t.Errorf("upgrade still pending after %s", tt.name)
				}
			})
		})
	}
}

// TestK8sHelmManager_UninstallReleaseNotFound verifies that only a missing release (driver.ErrReleaseNotFound, however
// wrapped) counts as a successful uninstall; unrelated "not found" errors still fail.
func TestK8sHelmManager_UninstallReleaseNotFound(t *testing.T) {
	tests := []struct {
		name         string
		uninstallErr error
		wantErr      bool
	}{
		{name: "success"},
		{name: "wrapped release not found", uninstallErr: fmt.Errorf("helm uninstall failed: %w", fmt.Errorf("uninstall: Release not loaded: %w", driver.ErrReleaseNotFound))},
		{name: "namespace not found", uninstallErr: errors.New(`namespaces "xmc" not found`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
			runner := &mockHelmActionRunner{uninstallErr: tt.uninstallErr}
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr.Close()

			if err := mgr.uninstallRelease(context.Background(), ref); (err != nil) != tt.wantErr {
				t.Errorf("uninstallRelease() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err := mgr.GetUpgradeError(ref); (err != nil) != tt.wantErr {
				t.Errorf("GetUpgradeError() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestK8sHelmManager_TeardownLockTimeoutStillDiscardsQueuedUpgrade verifies that if Delete1PComponent times out
// waiting for upgradeSem while another release operation holds the lock, it still discards the target component's
// queued upgrade so the worker does not install it after the ConfigMap entry was disabled.
func TestK8sHelmManager_TeardownLockTimeoutStillDiscardsQueuedUpgrade(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		other := ComponentRef{Namespace: "xmc", Name: "redpanda"}
		target := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
		gate := make(chan struct{})
		runner := &mockHelmActionRunner{upgradeGate: gate, upgradeStarted: make(chan struct{}, 1)}
		mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithDebounceDuration(10*time.Millisecond))
		defer mgr.Close()

		// Start an upgrade for `other` that holds upgradeSem on `gate`.
		if err := mgr.Ensure1PComponent(ctx, other, HelmComponentOptions{Ref: other, Enabled: true}); err != nil {
			t.Fatalf("Ensure1PComponent(other) failed: %v", err)
		}
		<-runner.upgradeStarted

		// Enqueue an upgrade for `target` while `other` holds the lock.
		if err := mgr.Ensure1PComponent(ctx, target, HelmComponentOptions{Ref: target, Enabled: true}); err != nil {
			t.Fatalf("Ensure1PComponent(target) failed: %v", err)
		}

		// Delete `target` with a context that cancels while waiting on upgradeSem.
		delCtx, cancel := context.WithCancel(ctx)
		deleteErr := make(chan error, 1)
		go func() { deleteErr <- mgr.Delete1PComponent(delCtx, target) }()
		synctest.Wait()
		cancel()
		if err := <-deleteErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("Delete1PComponent error = %v, want context.Canceled", err)
		}

		// Release `other`'s upgrade and let the worker drain. `target`'s queued upgrade must be skipped.
		close(gate)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		if diff := cmp.Diff([]string{"upgrade"}, runner.recordedOps()); diff != "" {
			t.Errorf("release operations mismatch (-want +got):\n%s", diff)
		}
		if mgr.isUpgradePending(target) {
			t.Error("expected target upgrade to be discarded after Delete1PComponent lock timeout")
		}
		if err := mgr.GetUpgradeError(target); !errors.Is(err, context.Canceled) {
			t.Errorf("GetUpgradeError(target) = %v, want context.Canceled preserved", err)
		}
	})
}
