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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// TestSetComponentIntent_DependencyRegistration runs the same rows against MemoryStore and
// SpannerStore so both reject intents whose dependencies are not registered with the
// FailedPrecondition error that the GitOps bridge reports as waiting on dependencies.
func TestSetComponentIntent_DependencyRegistration(t *testing.T) {
	tests := []struct {
		name          string
		registerDep   bool
		seedDependent bool
		deleteDep     bool
		wantRejected  bool
	}{
		{name: "registered dependency is accepted", registerDep: true},
		{name: "unregistered dependency is rejected", wantRejected: true},
		{name: "soft-deleted dependency is rejected", registerDep: true, deleteDep: true, wantRejected: true},
		{name: "unchanged intent with a deregistered dependency is rejected", registerDep: true, seedDependent: true, deleteDep: true, wantRejected: true},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					dep, dependent := fmt.Sprintf("registration-dep-%d", i), fmt.Sprintf("registration-dependent-%d", i)
					spec := intentSpec("v1.0.0", dep)
					seedRegistrationRow(ctx, t, s, dep, dependent, spec, tt.registerDep, tt.seedDependent, tt.deleteDep)

					_, err := s.SetComponentIntent(ctx, dependent, spec, nil, 1)
					if !tt.wantRejected {
						if err != nil {
							t.Fatalf("SetComponentIntent() error = %v, want nil", err)
						}
						return
					}
					if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), store.DependencyNotFoundMessagePrefix) {
						t.Errorf("SetComponentIntent() error = %v, want FailedPrecondition starting with %q", err, store.DependencyNotFoundMessagePrefix)
					}
				})
			}
		})
	}
}

// TestSetComponentIntent_ReRegisteredDependencyParity runs the same rows against MemoryStore and
// SpannerStore: once a dependency is soft-deleted with DELETION_POLICY_ORPHAN, the edge to it is
// gone in both stores, so re-registering the same name neither restores the edge nor creates a
// cycle with a former dependent.
func TestSetComponentIntent_ReRegisteredDependencyParity(t *testing.T) {
	tests := []struct {
		name string
		// reverse applies the re-registered dependency's intent declaring the former dependent
		// instead of re-applying the dependent's unchanged intent.
		reverse bool
		wantGen int64
	}{
		{name: "unchanged dependent intent re-links to the re-registered dependency", wantGen: 2},
		{name: "re-registered dependency may depend on a former dependent", reverse: true, wantGen: 2},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					dep, dependent := fmt.Sprintf("reregistered-dep-%d", i), fmt.Sprintf("reregistered-dependent-%d", i)
					seedRegistrationRow(ctx, t, s, dep, dependent, intentSpec("v1.0.0", dep), true, true, true)
					assertDependencies(ctx, t, s, dependent, nil)
					if _, err := s.SetComponentIntent(ctx, dep, intentSpec("v1.0.0"), nil, 1); err != nil {
						t.Fatalf("re-register %s error = %v", dep, err)
					}

					target, targetDep, gen := dependent, dep, int64(1)
					if tt.reverse {
						target, targetDep, gen = dep, dependent, 2
					}
					got, err := s.SetComponentIntent(ctx, target, intentSpec("v1.0.0", targetDep), nil, gen)
					if err != nil {
						t.Fatalf("SetComponentIntent(%s) error = %v", target, err)
					}
					if got.Generation != tt.wantGen {
						t.Errorf("SetComponentIntent(%s) generation = %d, want %d", target, got.Generation, tt.wantGen)
					}
					assertDependencies(ctx, t, s, target, []string{targetDep})
				})
			}
		})
	}
}

// assertDependencies fails t unless the stored record of name has exactly the dependencies want.
func assertDependencies(ctx context.Context, t *testing.T, s store.Store, name string, want []string) {
	t.Helper()
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(want, rec.Dependencies, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("dependencies of %s mismatch (-want +got):\n%s", name, diff)
	}
}

// seedRegistrationRow optionally registers dep, applies spec to dependent once, and soft-deletes
// dep with DELETION_POLICY_ORPHAN and force so that dependent is not deleted with it. The
// dependent's spec still declares dep, but its edge to the deleted dep is gone.
func seedRegistrationRow(ctx context.Context, t *testing.T, s store.Store, dep, dependent string, spec *componentspb.ComponentSpec, registerDep, seedDependent, deleteDep bool) {
	t.Helper()
	if registerDep {
		if _, err := s.SetComponentIntent(ctx, dep, intentSpec("v1.0.0"), nil, 1); err != nil {
			t.Fatalf("register dependency %s error = %v", dep, err)
		}
	}
	if seedDependent {
		if _, err := s.SetComponentIntent(ctx, dependent, spec, nil, 1); err != nil {
			t.Fatalf("seed dependent %s error = %v", dependent, err)
		}
	}
	if deleteDep {
		if err := s.ExecuteDeletion(ctx, dep, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
			t.Fatalf("ExecuteDeletion(%s) error = %v", dep, err)
		}
	}
}
