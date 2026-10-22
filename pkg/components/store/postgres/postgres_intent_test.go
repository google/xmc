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

package postgres

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/store"
)

const (
	testClusterAUID = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a01"
	testClusterBUID = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a02"
)

func TestSetComponentIntent_CRUDAndFastPath(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	writeCalls := 0
	s.beforeIntentWrite = func() { writeCalls++ }

	specV1 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "1.0.0",
		Description: "v1 description",
	}
	labels := map[string]string{"tier": "core"}

	created, err := s.SetComponentIntent(ctx, "components/comp-one", specV1, labels, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(create) unexpected error: %v", err)
	}
	if writeCalls != 1 {
		t.Fatalf("writeCalls = %d, want 1", writeCalls)
	}
	if created.Generation != 1 || created.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Fatalf("created = (gen=%d, state=%v), want (1, PENDING_RECONCILIATION)", created.Generation, created.State)
	}

	got, err := s.GetComponent(ctx, "comp-one")
	if err != nil {
		t.Fatalf("GetComponent(comp-one) unexpected error: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("SetComponentIntent vs GetComponent diff (-want +got):\n%s", diff)
	}

	// Repeating identical intent hits fast path without a read-write transaction.
	unchanged, err := s.SetComponentIntent(ctx, "comp-one", specV1, labels, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(unchanged) unexpected error: %v", err)
	}
	if writeCalls != 1 {
		t.Errorf("writeCalls after unchanged intent = %d, want 1 (fast path)", writeCalls)
	}
	if diff := cmp.Diff(created, unchanged, protocmp.Transform()); diff != "" {
		t.Errorf("unchanged record diff (-want +got):\n%s", diff)
	}

	// Updating spec advances generation to 2.
	specV2 := &componentspb.ComponentSpec{
		Type:        componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:     "1.1.0",
		Description: "v2 description",
	}
	updated, err := s.SetComponentIntent(ctx, "comp-one", specV2, labels, 2)
	if err != nil {
		t.Fatalf("SetComponentIntent(update) unexpected error: %v", err)
	}
	if writeCalls != 2 || updated.Generation != 2 {
		t.Errorf("updated = (writeCalls=%d, gen=%d), want (2, 2)", writeCalls, updated.Generation)
	}
	if !updated.CreateTime.Equal(created.CreateTime) {
		t.Errorf("CreateTime changed on update: got %v, want %v", updated.CreateTime, created.CreateTime)
	}
}

func TestSetComponentIntent_DependenciesAndCycles(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	baseSpec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.0.0",
	}

	// Unregistered dependency is rejected.
	withMissingDep := &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "1.0.0",
		Dependencies: []string{"missing-dep"},
	}
	if _, err := s.SetComponentIntent(ctx, "comp-parent", withMissingDep, nil, 1); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonDependencyNotRegistered {
		t.Fatalf("SetComponentIntent(missing-dep) = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonDependencyNotRegistered)
	}

	// Register dep-a and dep-b, then comp-parent depending on both.
	if _, err := s.SetComponentIntent(ctx, "dep-b", baseSpec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(dep-b) failed: %v", err)
	}
	if _, err := s.SetComponentIntent(ctx, "dep-a", baseSpec, nil, 1); err != nil {
		t.Fatalf("SetComponentIntent(dep-a) failed: %v", err)
	}
	parentSpec := &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "1.0.0",
		Dependencies: []string{"dep-b", "dep-a"},
	}
	parentRec, err := s.SetComponentIntent(ctx, "comp-parent", parentSpec, nil, 1)
	if err != nil {
		t.Fatalf("SetComponentIntent(comp-parent) failed: %v", err)
	}
	if diff := cmp.Diff([]string{"dep-a", "dep-b"}, parentRec.Dependencies); diff != "" {
		t.Errorf("Dependencies diff (-want +got):\n%s", diff)
	}

	// Introducing a cycle (dep-a -> comp-parent) is rejected.
	cycleSpec := &componentspb.ComponentSpec{
		Type:         componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:      "1.0.0",
		Dependencies: []string{"comp-parent"},
	}
	if _, err := s.SetComponentIntent(ctx, "dep-a", cycleSpec, nil, 2); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonDependencyCycle {
		t.Fatalf("SetComponentIntent(cycle) = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonDependencyCycle)
	}
}

func TestSetSourcedComponentIntent_OwnershipAndFence(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	specV1 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.0.0",
	}
	srcA1 := store.RequestSource{
		UID: "cr-uid-1", Generation: 1, Namespace: "xmc-system", ClusterUID: testClusterAUID, Epoch: 1,
	}

	// Sourced intent before DeclareClusterEpoch is rejected with CLUSTER_NOT_DECLARED.
	if _, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV1, nil, srcA1); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonClusterNotDeclared {
		t.Fatalf("before fence declaration = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonClusterNotDeclared)
	}

	if _, err := s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: testClusterAUID, Name: "cluster-a", Epoch: 1}); err != nil {
		t.Fatalf("DeclareClusterEpoch() failed: %v", err)
	}

	rec1, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV1, nil, srcA1)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(create) failed: %v", err)
	}
	if rec1.Generation != 1 || rec1.Owner == nil || rec1.Owner.UID != "cr-uid-1" || rec1.Owner.Generation != 1 {
		t.Fatalf("rec1 = (gen=%d, owner=%+v), want gen=1 owned by cr-uid-1@1", rec1.Generation, rec1.Owner)
	}

	// Owner-only write: newer CR generation with identical spec updates Owner.Generation without bumping Component.Generation.
	srcA2 := srcA1
	srcA2.Generation = 2
	recOwnerOnly, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV1, nil, srcA2)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(owner-only) failed: %v", err)
	}
	if recOwnerOnly.Generation != 1 || recOwnerOnly.Owner.Generation != 2 {
		t.Errorf("recOwnerOnly = (gen=%d, ownerGen=%d), want (1, 2)", recOwnerOnly.Generation, recOwnerOnly.Owner.Generation)
	}

	// Older CR generation with changed spec is rejected with STALE_GENERATION.
	specV2 := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "1.1.0",
	}
	if _, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV2, nil, srcA1); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonStaleGeneration {
		t.Fatalf("stale generation = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonStaleGeneration)
	}

	// Different namespace is rejected with SOURCE_NAMESPACE_CONFLICT.
	otherNS := srcA2
	otherNS.Namespace = "other-ns"
	if _, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV2, nil, otherNS); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonSourceNamespaceConflict {
		t.Fatalf("namespace conflict = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonSourceNamespaceConflict)
	}

	// Incarnation takeover in same namespace & cluster succeeds when ExpectedOwnerUID matches (R5).
	newIncarnation := store.RequestSource{
		UID: "cr-uid-2", Generation: 1, Namespace: "xmc-system", ClusterUID: testClusterAUID, Epoch: 1,
		ExpectedOwnerUID: "cr-uid-1",
	}
	adopted, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV2, nil, newIncarnation)
	if err != nil {
		t.Fatalf("SetSourcedComponentIntent(adopt) failed: %v", err)
	}
	if adopted.Generation != 2 || adopted.Owner.UID != "cr-uid-2" {
		t.Errorf("adopted = (gen=%d, ownerUID=%q), want (2, cr-uid-2)", adopted.Generation, adopted.Owner.UID)
	}

	// Fenced-out cluster B is rejected on the fast path with CLUSTER_FENCED.
	writeCalls := 0
	s.beforeIntentWrite = func() { writeCalls++ }
	fencedSrc := store.RequestSource{
		UID: "cr-uid-2", Generation: 1, Namespace: "xmc-system", ClusterUID: testClusterBUID, Epoch: 1,
	}
	if _, err := s.SetSourcedComponentIntent(ctx, "sourced-comp", specV2, nil, fencedSrc); status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonClusterFenced {
		t.Fatalf("fenced cluster = (%v, %q), want (FailedPrecondition, %q)", status.Code(err), store.ErrorInfoReason(err), store.ReasonClusterFenced)
	}
	if writeCalls != 0 {
		t.Errorf("writeCalls on fast-path fence rejection = %d, want 0", writeCalls)
	}
}
