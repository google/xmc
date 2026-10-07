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

package store_test

import (
	"context"
	"fmt"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// idempotencyDependency and idempotencyDependencyB are registered in every backend before the rows
// run so rows can declare dependencies that SpannerStore accepts.
const (
	idempotencyDependency  = "intent-idempotency-dep"
	idempotencyDependencyB = "intent-idempotency-dep-b"
)

func intentSpec(version string, deps ...string) *componentspb.ComponentSpec {
	return &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      version,
		Description:  "idempotency test component",
		Dependencies: deps,
	}
}

// TestSetComponentIntent_Idempotency runs the same rows against MemoryStore and SpannerStore so both
// implementations keep identical semantics for unchanged and changed intents. Each row seeds a
// component at seedGen with seedDeps, drives it to ENABLED (or DISABLED through the deactivation
// API), and re-applies an intent.
func TestSetComponentIntent_Idempotency(t *testing.T) {
	baseLabels := map[string]string{"tier": "backend"}
	enabled := componentspb.ComponentState_COMPONENT_STATE_ENABLED
	disabled := componentspb.ComponentState_COMPONENT_STATE_DISABLED
	tests := []struct {
		name       string
		seedGen    int64
		seedDeps   []string
		seedState  componentspb.ComponentState
		spec       *componentspb.ComponentSpec
		labels     map[string]string
		desiredGen int64
		wantGen    int64
		wantNoop   bool
	}{
		{name: "same generation and spec is a no-op", seedGen: 3, seedState: enabled, spec: intentSpec("v1.0.0"), labels: baseLabels, desiredGen: 3, wantGen: 3, wantNoop: true},
		{name: "unspecified generation and equal spec is a no-op", seedGen: 3, seedState: enabled, spec: intentSpec("v1.0.0"), labels: baseLabels, desiredGen: 0, wantGen: 3, wantNoop: true},
		{name: "inflated store generation and equal spec is a no-op", seedGen: 5000, seedState: enabled, spec: intentSpec("v1.0.0"), labels: baseLabels, desiredGen: 3, wantGen: 5000, wantNoop: true},
		{name: "equal dependencies are a no-op", seedGen: 3, seedDeps: []string{idempotencyDependency}, seedState: enabled, spec: intentSpec("v1.0.0", idempotencyDependency), labels: baseLabels, desiredGen: 3, wantGen: 3, wantNoop: true},
		{name: "disabled component stays disabled", seedGen: 3, seedState: disabled, spec: intentSpec("v1.0.0"), labels: baseLabels, desiredGen: 3, wantGen: 3, wantNoop: true},
		{name: "newer generation and equal spec bumps to desired generation", seedGen: 3, seedState: enabled, spec: intentSpec("v1.0.0"), labels: baseLabels, desiredGen: 4, wantGen: 4},
		{name: "changed spec and lower generation bumps by one", seedGen: 5000, seedState: enabled, spec: intentSpec("v1.1.0"), labels: baseLabels, desiredGen: 3, wantGen: 5001},
		{name: "changed spec and same generation bumps by one", seedGen: 3, seedState: enabled, spec: intentSpec("v1.1.0"), labels: baseLabels, desiredGen: 3, wantGen: 4},
		{name: "changed labels bump by one", seedGen: 3, seedState: enabled, spec: intentSpec("v1.0.0"), labels: map[string]string{"tier": "frontend"}, desiredGen: 3, wantGen: 4},
		{name: "added dependency bumps by one", seedGen: 3, seedState: enabled, spec: intentSpec("v1.0.0", idempotencyDependency), labels: baseLabels, desiredGen: 3, wantGen: 4},
		// The store compares the persisted spec exactly; the CMS API canonicalizes prefixes first.
		{name: "dependency order change is a spec change", seedGen: 3, seedDeps: []string{idempotencyDependency, idempotencyDependencyB}, seedState: enabled, spec: intentSpec("v1.0.0", idempotencyDependencyB, idempotencyDependency), labels: baseLabels, desiredGen: 3, wantGen: 4},
		{name: "dependency prefix change is a spec change", seedGen: 3, seedDeps: []string{idempotencyDependency}, seedState: enabled, spec: intentSpec("v1.0.0", "components/"+idempotencyDependency), labels: baseLabels, desiredGen: 3, wantGen: 4},
	}
	backends := []struct {
		name     string
		newStore func(t *testing.T) store.Store
	}{
		{name: "memory", newStore: func(*testing.T) store.Store { return store.NewMemoryStore() }},
		{name: "spanner", newStore: func(t *testing.T) store.Store {
			s, _ := setupTestStore(context.Background(), t)
			return s
		}},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			seedIntentComponent(ctx, t, s, idempotencyDependency, intentSpec("v1.0.0"), 1, nil, enabled)
			seedIntentComponent(ctx, t, s, idempotencyDependencyB, intentSpec("v1.0.0"), 1, nil, enabled)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("intent-idempotency-%d", i)
					before := seedIntentComponent(ctx, t, s, name, intentSpec("v1.0.0", tt.seedDeps...), tt.seedGen, baseLabels, tt.seedState)

					got, err := s.SetComponentIntent(ctx, name, tt.spec, tt.labels, tt.desiredGen)
					if err != nil {
						t.Fatalf("SetComponentIntent() error = %v", err)
					}
					if got.Generation != tt.wantGen || got.Spec.GetGeneration() != tt.wantGen {
						t.Errorf("generation = %d, spec.generation = %d, want %d", got.Generation, got.Spec.GetGeneration(), tt.wantGen)
					}
					wantState := componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
					if tt.wantNoop {
						wantState = tt.seedState
						if !got.UpdateTime.Equal(before.UpdateTime) || got.Etag != before.Etag {
							t.Errorf("no-op changed UpdateTime %v -> %v or Etag %q -> %q", before.UpdateTime, got.UpdateTime, before.Etag, got.Etag)
						}
					}
					stored, err := s.GetComponent(ctx, name)
					if err != nil {
						t.Fatalf("GetComponent() error = %v", err)
					}
					for _, rec := range []*store.ComponentRecord{got, stored} {
						if rec.State != wantState || rec.Status.GetState() != wantState {
							t.Errorf("state = %v, status.state = %v, want %v", rec.State, rec.Status.GetState(), wantState)
						}
					}
					if stored.Generation != tt.wantGen {
						t.Errorf("stored generation = %d, want %d", stored.Generation, tt.wantGen)
					}
				})
			}
		})
	}
}

// seedIntentComponent creates name at generation gen with spec and labels, records it as observed
// and ENABLED, drives it to DISABLED through the deactivation API when state is DISABLED, and
// returns the stored record.
func seedIntentComponent(ctx context.Context, t *testing.T, s store.Store, name string, spec *componentspb.ComponentSpec, gen int64, labels map[string]string, state componentspb.ComponentState) *store.ComponentRecord {
	t.Helper()
	if _, err := s.SetComponentIntent(ctx, name, spec, labels, gen); err != nil {
		t.Fatalf("seed SetComponentIntent(%s, gen %d) error = %v", name, gen, err)
	}
	if _, err := s.UpdateObservedGeneration(ctx, name, gen, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "Ready"); err != nil {
		t.Fatalf("seed UpdateObservedGeneration(%s) error = %v", name, err)
	}
	if state == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		if _, err := s.PrepareDeactivation(ctx, name, false); err != nil {
			t.Fatalf("seed PrepareDeactivation(%s) error = %v", name, err)
		}
		if err := s.CompleteDeactivation(ctx, name, false); err != nil {
			t.Fatalf("seed CompleteDeactivation(%s) error = %v", name, err)
		}
	}
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("seed GetComponent(%s) error = %v", name, err)
	}
	if rec.State != state {
		t.Fatalf("seed state of %s = %v, want %v", name, rec.State, state)
	}
	return rec
}
