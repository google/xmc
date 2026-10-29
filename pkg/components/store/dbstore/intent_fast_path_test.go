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

// Spanner emulator tests for SpannerStore.SetComponentIntent read-only fast path vs read-write
// transaction fallback.
// Production target: spanner_store.go (SetComponentIntent and readIdenticalActiveIntent).
// Test type: hermetic Spanner emulator test in package store_test. Kept in a dedicated file to
// avoid further growing spanner_store_test.go (~1,400 lines) and to isolate fast-path fallback
// counting via SetBeforeIntentWriteForTest.
package dbstore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	spannerstorage "xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
	"xmanager_cloud/pkg/components/store/dependency"
)

// TestSpannerStore_SetComponentIntent_ReadOnlyFastPath seeds a component at generation 1 that
// depends on a registered component (or on nothing for noDep rows), re-applies an intent, and
// checks whether SetComponentIntent fell back to its read-write transaction.
func TestSpannerStore_SetComponentIntent_ReadOnlyFastPath(t *testing.T) {
	labels := map[string]string{"tier": "backend"}
	tests := []struct {
		name          string
		version       string
		labels        map[string]string
		desiredGen    int64
		noDep         bool
		deleteDep     bool
		wantFallbacks int
		wantGen       int64
		wantCode      codes.Code
	}{
		{name: "unchanged intent skips the read-write transaction", version: "v1.0.0", labels: labels, desiredGen: 1, wantGen: 1},
		{name: "unchanged intent with unspecified generation skips the read-write transaction", version: "v1.0.0", labels: labels, desiredGen: 0, wantGen: 1},
		{name: "unchanged intent without dependencies skips the read-write transaction", version: "v1.0.0", labels: labels, desiredGen: 1, noDep: true, wantGen: 1},
		{name: "changed spec falls back", version: "v1.1.0", labels: labels, desiredGen: 1, wantFallbacks: 1, wantGen: 2},
		{name: "changed labels fall back", version: "v1.0.0", labels: map[string]string{"tier": "frontend"}, desiredGen: 1, wantFallbacks: 1, wantGen: 2},
		{name: "newer desired generation falls back", version: "v1.0.0", labels: labels, desiredGen: 5, wantFallbacks: 1, wantGen: 5},
		{name: "deregistered dependency falls back and is rejected", version: "v1.0.0", labels: labels, desiredGen: 1, deleteDep: true, wantFallbacks: 1, wantGen: 1, wantCode: codes.FailedPrecondition},
	}
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)
	// Subtests are intentionally sequential (no t.Parallel) because they share one store and hook.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep, name := fmt.Sprintf("fast-path-dep-%d", i), fmt.Sprintf("fast-path-%d", i)
			deps := []string{dep}
			if tt.noDep {
				deps = nil
			}
			before := seedFastPathComponent(ctx, t, s, name, labels, deps)
			if tt.deleteDep {
				if err := s.ExecuteDeletion(ctx, dep, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
					t.Fatalf("ExecuteDeletion(%s) error = %v", dep, err)
				}
			}
			fallbacks := 0
			s.SetBeforeIntentWriteForTest(func() { fallbacks++ })
			defer s.SetBeforeIntentWriteForTest(nil)

			got, err := s.SetComponentIntent(ctx, name, intentSpec(tt.version, deps...), tt.labels, tt.desiredGen)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("SetComponentIntent() error = %v, want code %v", err, tt.wantCode)
			}
			if fallbacks != tt.wantFallbacks {
				t.Errorf("read-write fallbacks = %d, want %d", fallbacks, tt.wantFallbacks)
			}
			if err == nil && tt.wantFallbacks == 0 {
				if diff := cmp.Diff(before, got, protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("fast path returned a record different from the stored one (-before +got):\n%s", diff)
				}
			}
			assertStoredGeneration(ctx, t, s, name, tt.wantGen)
		})
	}
}

// TestSpannerStore_SetComponentIntent_ConcurrentChangeBeforeFallback injects a write from another
// store between the fast-path read and the read-write fallback, so the fallback must re-decide
// against the committed state instead of the stale read.
func TestSpannerStore_SetComponentIntent_ConcurrentChangeBeforeFallback(t *testing.T) {
	labels := map[string]string{"tier": "backend"}
	tests := []struct {
		name       string
		concurrent func(ctx context.Context, other store.Store, dep, name string) error
		wantGen    int64
		wantCode   codes.Code
	}{
		{
			name: "concurrent identical write turns the fallback into a no-op",
			concurrent: func(ctx context.Context, other store.Store, dep, name string) error {
				_, err := other.SetComponentIntent(ctx, name, intentSpec("v1.1.0", dep), labels, 0)
				return err
			},
			wantGen: 2,
		},
		{
			name: "concurrent dependency deletion makes the fallback reject",
			concurrent: func(ctx context.Context, other store.Store, dep, _ string) error {
				return other.ExecuteDeletion(ctx, dep, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true)
			},
			wantGen:  1,
			wantCode: codes.FailedPrecondition,
		},
	}
	ctx := context.Background()
	s, client := setupTestStore(ctx, t)
	otherSt, err := spannerstorage.NewStorage(client)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	other, err := dbstore.New(otherSt)
	if err != nil {
		t.Fatalf("dbstore.New() failed: %v", err)
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep, name := fmt.Sprintf("concurrent-fast-path-dep-%d", i), fmt.Sprintf("concurrent-fast-path-%d", i)
			seedFastPathComponent(ctx, t, s, name, labels, []string{dep})
			s.SetBeforeIntentWriteForTest(func() {
				if err := tt.concurrent(ctx, other, dep, name); err != nil {
					t.Errorf("concurrent write error = %v", err)
				}
			})
			defer s.SetBeforeIntentWriteForTest(nil)

			_, err := s.SetComponentIntent(ctx, name, intentSpec("v1.1.0", dep), labels, 0)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("SetComponentIntent() error = %v, want code %v", err, tt.wantCode)
			}
			if err != nil && !strings.HasPrefix(status.Convert(err).Message(), dependency.DependencyNotFoundMessagePrefix) {
				t.Errorf("SetComponentIntent() error = %v, want message starting with %q", err, dependency.DependencyNotFoundMessagePrefix)
			}
			assertStoredGeneration(ctx, t, s, name, tt.wantGen)
		})
	}
}

// seedFastPathComponent registers every dependency in deps, creates name at generation 1 depending
// on deps with labels, and returns the stored record of name.
func seedFastPathComponent(ctx context.Context, t *testing.T, s store.Store, name string, labels map[string]string, deps []string) *store.ComponentRecord {
	t.Helper()
	for _, dep := range deps {
		if _, err := s.SetComponentIntent(ctx, dep, intentSpec("v1.0.0"), nil, 1); err != nil {
			t.Fatalf("seed SetComponentIntent(%s) error = %v", dep, err)
		}
	}
	if _, err := s.SetComponentIntent(ctx, name, intentSpec("v1.0.0", deps...), labels, 1); err != nil {
		t.Fatalf("seed SetComponentIntent(%s) error = %v", name, err)
	}
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("seed GetComponent(%s) error = %v", name, err)
	}
	return rec
}

func assertStoredGeneration(ctx context.Context, t *testing.T, s store.Store, name string, want int64) {
	t.Helper()
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if stored.Generation != want {
		t.Errorf("stored generation of %s = %d, want %d", name, stored.Generation, want)
	}
}
