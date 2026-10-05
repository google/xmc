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

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/kubernetes/fake"

	"helm.sh/helm/v3/pkg/release"
)

// TestK8sHelmManager_TeardownForgetsState verifies that Delete keeps the component recorded as inactive with its chart
// path override, while Purge and Orphan forget it entirely; all three drop the actuated generation.
func TestK8sHelmManager_TeardownForgetsState(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	key := componentKey(ref.Namespace, ref.Name)
	type state struct {
		Active, Known, Actuated, HasChartPath bool
	}
	tests := []struct {
		name     string
		teardown func(context.Context, *K8sHelmManager) error
		want     state
	}{
		{name: "Delete1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Delete1PComponent(ctx, ref) }, want: state{Known: true, HasChartPath: true}},
		{name: "Purge1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Purge1PComponent(ctx, ref) }},
		{name: "Orphan1PComponent", teardown: func(ctx context.Context, m *K8sHelmManager) error { return m.Orphan1PComponent(ctx, ref) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(&mockHelmActionRunner{}), WithSyncExecution(true))
			defer mgr.Close()
			if err := mgr.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true, ChartPath: "components/metrics"}); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			mgr.RecordActuation(ref, 1)
			if err := tt.teardown(ctx, mgr); err != nil {
				t.Fatalf("%s failed: %v", tt.name, err)
			}

			mgr.mu.RLock()
			defer mgr.mu.RUnlock()
			var got state
			got.Active, got.Known = mgr.activeState[key]
			_, got.Actuated = mgr.actuatedGenerations[key]
			_, got.HasChartPath = mgr.componentChartPaths[key]
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("state after %s mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

func TestK8sHelmManager_ActuationTracking(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}
	mgr := NewK8sHelmManager(client, "xmc", "xmanager-cloud",
		WithActionRunner(runner),
		WithSyncExecution(true),
	)
	defer mgr.Close()

	ref := ComponentRef{Namespace: "xmc", Name: "metrics"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true}

	if mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component not to be actuated before Ensure1PComponent")
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}
	mgr.RecordActuation(ref, 1)

	if !mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component to be actuated at generation 1")
	}
	if mgr.IsComponentActuated(ref, 2) {
		t.Error("expected component not to be actuated at generation 2")
	}

	// After deletion, actuation record should be cleared
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}
	if mgr.IsComponentActuated(ref, 1) {
		t.Error("expected component actuation to be cleared after deletion")
	}
}

func TestK8sHelmManager_ColdStartHydration(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	runner := &mockHelmActionRunner{}

	// First manager persists active component values into xmc-component-values ConfigMap
	mgr1 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	if err := mgr1.Ensure1PComponent(ctx, ref, HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}); err != nil {
		t.Fatalf("mgr1 Ensure1PComponent failed: %v", err)
	}
	_ = mgr1.Close()

	// Simulate controller restart with a new K8sHelmManager instance
	mgr2 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer mgr2.Close()

	if err := mgr2.HydrateFromCluster(ctx); err != nil {
		t.Fatalf("HydrateFromCluster failed: %v", err)
	}
	if !mgr2.IsComponentActive(ref) {
		t.Fatal("expected ingestion-gateway to be active after HydrateFromCluster")
	}

	if err := mgr2.HydrateComponentState(ctx, ref, 3, true); err != nil {
		t.Fatalf("HydrateComponentState failed: %v", err)
	}
	if !mgr2.IsComponentActuated(ref, 3) {
		t.Fatal("expected ingestion-gateway to be marked actuated at generation 3 after HydrateComponentState")
	}
}

// TestK8sHelmManager_ColdStartHydration_VerifiesRelease covers the cold-start case where the values ConfigMap and a
// Ready=True CR survive a restart, but the component's Helm release was never successfully installed.
func TestK8sHelmManager_ColdStartHydration_VerifiesRelease(t *testing.T) {
	ref := ComponentRef{Namespace: "xmc", Name: "ingestion-gateway"}
	opts := HelmComponentOptions{Ref: ref, Enabled: true, ImageTag: "v1.0.0"}
	type result struct {
		Active           bool
		Actuated         bool
		StatusCalls      int
		UpgradeTriggered bool
	}
	tests := []struct {
		name               string
		initialUpgradeErr  error
		releaseStatus      release.Status
		statusErr          error
		observedGeneration int64
		crReady            bool
		want               result
	}{
		{
			name:               "deployed release trusts Ready CR without re-upgrade",
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "missing release is not trusted and next Ensure installs it",
			initialUpgradeErr:  errors.New("install failed"),
			observedGeneration: 3, crReady: true,
			want: result{StatusCalls: 1, UpgradeTriggered: true},
		},
		{
			name:               "failed release is not trusted",
			releaseStatus:      release.StatusFailed,
			observedGeneration: 3, crReady: true,
			want: result{StatusCalls: 1, UpgradeTriggered: true},
		},
		{
			name:               "transient status error trusts Ready CR",
			statusErr:          errors.New("connection refused"),
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "pending release left by a crashed bridge trusts Ready CR",
			releaseStatus:      release.StatusPendingUpgrade,
			observedGeneration: 3, crReady: true,
			want: result{Active: true, Actuated: true, StatusCalls: 1},
		},
		{
			name:               "unready CR skips release verification",
			observedGeneration: 3, crReady: false,
			want: result{UpgradeTriggered: true},
		},
		{
			name:               "deactivation hydration skips release verification",
			observedGeneration: 0, crReady: true,
			want: result{Active: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientset()
			runner := &mockHelmActionRunner{upgradeErr: tt.initialUpgradeErr}

			// Simulate the pre-restart controller persisting values (and possibly failing the install).
			mgr1 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			_ = mgr1.Ensure1PComponent(ctx, ref, opts)
			_ = mgr1.Close()

			runner.mu.Lock()
			runner.upgradeErr, runner.releaseStatus, runner.statusErr = nil, tt.releaseStatus, tt.statusErr
			runner.statusCalls = 0
			upgradesBefore := runner.upgradeCalls
			runner.mu.Unlock()

			mgr2 := NewK8sHelmManager(client, "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
			defer mgr2.Close()
			if err := mgr2.HydrateComponentState(ctx, ref, tt.observedGeneration, tt.crReady); err != nil {
				t.Fatalf("HydrateComponentState failed: %v", err)
			}
			// Actuation is always checked at the CR's current generation (3), independent of the hydrated observedGeneration.
			got := result{Active: mgr2.IsComponentActive(ref), Actuated: mgr2.IsComponentActuated(ref, 3)}
			if err := mgr2.Ensure1PComponent(ctx, ref, opts); err != nil {
				t.Fatalf("Ensure1PComponent failed: %v", err)
			}
			runner.mu.Lock()
			got.StatusCalls = runner.statusCalls
			got.UpgradeTriggered = runner.upgradeCalls > upgradesBefore
			runner.mu.Unlock()

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("cold-start hydration mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
