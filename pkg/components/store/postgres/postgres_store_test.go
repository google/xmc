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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

func TestNewPostgresStore(t *testing.T) {
	t.Run("NilPoolReturnsInvalidArgument", func(t *testing.T) {
		s, err := NewPostgresStore(nil)
		if err == nil {
			t.Fatalf("NewPostgresStore(nil) = %v, want error", s)
		}
		if !errors.Is(err, grpcerr.ErrInvalidArgument) {
			t.Errorf("errors.Is(%v, grpcerr.ErrInvalidArgument) = false, want true", err)
		}
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Errorf("status.Code(%v) = %v, want %v", err, got, codes.InvalidArgument)
		}
	})

	t.Run("BindsPool", func(t *testing.T) {
		pool := &pgxpool.Pool{}
		s, err := NewPostgresStore(pool)
		if err != nil {
			t.Fatalf("NewPostgresStore(pool) unexpected error: %v", err)
		}
		if s == nil || s.pool != pool {
			t.Errorf("NewPostgresStore(pool) = %+v, want pool %p", s, pool)
		}
	})
}

func TestPostgresStore_Unimplemented(t *testing.T) {
	s, err := NewPostgresStore(&pgxpool.Pool{})
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "SetComponentIntent",
			call: func() error {
				_, err := s.SetComponentIntent(ctx, "comp-a", nil, nil, 1)
				return err
			},
		},
		{
			name: "SetSourcedComponentIntent",
			call: func() error {
				_, err := s.SetSourcedComponentIntent(ctx, "comp-a", nil, nil, store.RequestSource{})
				return err
			},
		},
		{
			name: "PrepareDeactivation",
			call: func() error {
				_, err := s.PrepareDeactivation(ctx, "comp-a", false)
				return err
			},
		},
		{
			name: "CompleteDeactivation",
			call: func() error {
				return s.CompleteDeactivation(ctx, "comp-a", false)
			},
		},
		{
			name: "UpdateObservedGeneration",
			call: func() error {
				_, err := s.UpdateObservedGeneration(ctx, "comp-a", 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, "ok")
				return err
			},
		},
		{
			name: "ExecuteDeletion",
			call: func() error {
				return s.ExecuteDeletion(ctx, "comp-a", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
			},
		},
		{
			name: "ResolveComponentSnapshot",
			call: func() error {
				_, _, err := s.ResolveComponentSnapshot(ctx, "comp-a", 1)
				return err
			},
		},
		{
			name: "GetPlatformHealth",
			call: func() error {
				_, err := s.GetPlatformHealth(ctx, "", 10, "")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, codes.Unimplemented, err)
			}
		})
	}
}

func seedTestComponent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id int64, name string, compType componentspb.ComponentType, version string, state componentspb.ComponentState, gen int64, obsGen *int64, labels map[string]string, owner *store.SourceOwner) time.Time {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	spec := &componentspb.ComponentSpec{
		Type:       compType,
		Version:    version,
		Generation: gen,
	}
	compProto := &componentspb.Component{
		Name:   naming.FullComponentName(name),
		Spec:   spec,
		Labels: labels,
	}
	configBytes, err := proto.Marshal(compProto)
	if err != nil {
		t.Fatalf("proto.Marshal(Component) failed: %v", err)
	}
	accessProto := &componentspb.ComponentAccessStatus{
		State:         state,
		HealthMessage: "healthy",
	}
	if obsGen != nil {
		accessProto.ObservedGeneration = *obsGen
	}
	accessBytes, err := proto.Marshal(accessProto)
	if err != nil {
		t.Fatalf("proto.Marshal(ComponentAccessStatus) failed: %v", err)
	}
	const insertSQL = `INSERT INTO Components (ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`
	if _, err := pool.Exec(ctx, insertSQL, id, name, configBytes, accessBytes, int64(state), gen, obsGen, now); err != nil {
		t.Fatalf("insert component %q failed: %v", name, err)
	}
	if owner != nil {
		const ownerSQL = `UPDATE Components SET SourceUid = $2, SourceGeneration = $3, SourceNamespace = $4, SourceClusterUid = $5, SourceEpoch = $6, SourceLastSeenTime = $7 WHERE ComponentId = $1`
		if _, err := pool.Exec(ctx, ownerSQL, id, owner.UID, owner.Generation, owner.Namespace, owner.ClusterUID, owner.Epoch, now); err != nil {
			t.Fatalf("update component owner %q failed: %v", name, err)
		}
	}
	return now
}

func seedTestDependency(ctx context.Context, t *testing.T, pool *pgxpool.Pool, compID, depID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO ComponentDependencies (ComponentId, DependencyComponentId) VALUES ($1, $2)`, compID, depID); err != nil {
		t.Fatalf("insert dependency (%d -> %d) failed: %v", compID, depID, err)
	}
}

func TestGetComponentAndGetComponentByID(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	obsGen := int64(2)
	owner := &store.SourceOwner{
		UID: "uid-1", Generation: 3, Namespace: "xmc-system", ClusterUID: "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a01", Epoch: 1,
	}
	seedTestComponent(ctx, t, pool, 10, "dep-b", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "0.8.0", componentspb.ComponentState_COMPONENT_STATE_ENABLED, 1, nil, nil, nil)
	seedTestComponent(ctx, t, pool, 20, "dep-a", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, "0.6.0", componentspb.ComponentState_COMPONENT_STATE_ENABLED, 1, nil, nil, nil)
	seedTestComponent(ctx, t, pool, 30, "dep-deleted", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "1.0.0", componentspb.ComponentState_COMPONENT_STATE_DELETED, 1, nil, nil, nil)
	ts := seedTestComponent(ctx, t, pool, 100, "trainer-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "1.8.0", componentspb.ComponentState_COMPONENT_STATE_ENABLED, 2, &obsGen, map[string]string{"env": "prod"}, owner)
	seedTestDependency(ctx, t, pool, 100, 10)
	seedTestDependency(ctx, t, pool, 100, 20)
	seedTestDependency(ctx, t, pool, 100, 30)

	// GetComponent by full resource name returns active dependencies sorted alphabetically.
	rec, err := s.GetComponent(ctx, "components/trainer-comp")
	if err != nil {
		t.Fatalf("GetComponent() unexpected error: %v", err)
	}
	if rec.ComponentID != 100 || rec.Name != "trainer-comp" || rec.Generation != 2 {
		t.Errorf("GetComponent() = (%d, %q, gen=%d), want (100, trainer-comp, gen=2)", rec.ComponentID, rec.Name, rec.Generation)
	}
	if rec.ObservedGeneration == nil || *rec.ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %v, want 2", rec.ObservedGeneration)
	}
	if diff := cmp.Diff([]string{"dep-a", "dep-b"}, rec.Dependencies); diff != "" {
		t.Errorf("Dependencies diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(owner, rec.Owner); diff != "" {
		t.Errorf("Owner diff (-want +got):\n%s", diff)
	}
	if !rec.SourceLastSeenTime.Equal(ts) {
		t.Errorf("SourceLastSeenTime = %v, want %v", rec.SourceLastSeenTime, ts)
	}
	if rec.Etag != store.GenerateEtag(rec) {
		t.Errorf("Etag = %q, want %q", rec.Etag, store.GenerateEtag(rec))
	}

	// GetComponentByID returns identical record.
	recByID, err := s.GetComponentByID(ctx, 100)
	if err != nil {
		t.Fatalf("GetComponentByID(100) unexpected error: %v", err)
	}
	if diff := cmp.Diff(rec, recByID, protocmp.Transform()); diff != "" {
		t.Errorf("GetComponent vs GetComponentByID diff (-want +got):\n%s", diff)
	}

	// Soft-deleted component is NotFound for both GetComponent and GetComponentByID.
	if _, err := s.GetComponent(ctx, "dep-deleted"); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponent(dep-deleted) code = %v, want NotFound", status.Code(err))
	}
	if _, err := s.GetComponentByID(ctx, 30); status.Code(err) != codes.NotFound {
		t.Errorf("GetComponentByID(30) code = %v, want NotFound", status.Code(err))
	}

	// Invalid arguments.
	if _, err := s.GetComponent(ctx, ""); status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetComponent('') code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := s.GetComponentByID(ctx, 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetComponentByID(0) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestListComponentsAndValidateRequirement(t *testing.T) {
	ctx := context.Background()
	_, pool := postgrestest.Setup(ctx, t)
	s, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatalf("NewPostgresStore() failed: %v", err)
	}

	seedTestComponent(ctx, t, pool, 1, "zebra", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "0.8.1", componentspb.ComponentState_COMPONENT_STATE_ENABLED, 1, nil, nil, nil)
	seedTestComponent(ctx, t, pool, 2, "alpha", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, "0.5.0", componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, 1, nil, nil, nil)
	seedTestComponent(ctx, t, pool, 3, "deleted-comp", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "1.0.0", componentspb.ComponentState_COMPONENT_STATE_DELETED, 1, nil, nil, nil)
	seedTestDependency(ctx, t, pool, 1, 2)

	list, err := s.ListComponents(ctx)
	if err != nil {
		t.Fatalf("ListComponents() unexpected error: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zebra" {
		t.Fatalf("ListComponents() = %+v, want [alpha, zebra]", list)
	}
	if diff := cmp.Diff([]string{"alpha"}, list[1].Dependencies); diff != "" {
		t.Errorf("zebra dependencies diff (-want +got):\n%s", diff)
	}

	// Satisfied requirement.
	res, err := s.ValidateComponentRequirement(ctx, "zebra", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "0.8.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement(zebra) unexpected error: %v", err)
	}
	if !res.IsSatisfied || res.SatisfyingComponentName != "zebra" {
		t.Errorf("ValidateComponentRequirement(zebra) = %+v, want satisfied", res)
	}

	// Missing component returns IsSatisfied=false without error.
	missingRes, err := s.ValidateComponentRequirement(ctx, "nonexistent", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "0.8.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement(nonexistent) unexpected error: %v", err)
	}
	if missingRes.IsSatisfied {
		t.Errorf("ValidateComponentRequirement(nonexistent) = %+v, want unsatisfied", missingRes)
	}

	// Inactive component returns IsSatisfied=false.
	inactiveRes, err := s.ValidateComponentRequirement(ctx, "alpha", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, "0.5.0")
	if err != nil {
		t.Fatalf("ValidateComponentRequirement(alpha) unexpected error: %v", err)
	}
	if inactiveRes.IsSatisfied {
		t.Errorf("ValidateComponentRequirement(alpha) = %+v, want unsatisfied", inactiveRes)
	}

	// Invalid minVersion returns InvalidArgument.
	if _, err := s.ValidateComponentRequirement(ctx, "zebra", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "not-semver"); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateComponentRequirement(not-semver) code = %v, want InvalidArgument", status.Code(err))
	}
}
