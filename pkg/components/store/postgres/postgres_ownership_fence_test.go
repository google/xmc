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

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/ownership"
	postgresstore "xmanager_cloud/pkg/components/store/postgres"
)

const (
	prodClusterUID = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a01"
	drClusterUID   = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a02"
)

func setupTestStore(ctx context.Context, t *testing.T) (*postgresstore.PostgresStore, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(ctx, t)
	s, err := postgresstore.NewPostgresStore(essPool)
	if err != nil {
		t.Fatalf("NewPostgresStore() error = %v", err)
	}
	return s, essPool
}

func TestDeclareClusterEpoch_Sequence(t *testing.T) {
	ctx := context.Background()
	s, pool := setupTestStore(ctx, t)

	prod := func(name string, epoch int64) store.ClusterIdentity {
		return store.ClusterIdentity{UID: prodClusterUID, Name: name, Epoch: epoch}
	}
	dr := func(epoch int64) store.ClusterIdentity {
		return store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: epoch}
	}

	steps := []struct {
		name        string
		cluster     store.ClusterIdentity
		wantOutcome store.DeclarationOutcome
		wantReason  string
		wantActive  store.ClusterIdentity
	}{
		{name: "first declaration creates the fence", cluster: prod("prod", 1), wantOutcome: store.DeclarationCreated},
		{name: "active cluster refreshes its epoch", cluster: prod("prod", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster refreshes under a new name", cluster: prod("prod-east", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster raises its epoch", cluster: prod("prod-east", 3), wantOutcome: store.DeclarationRaised},
		{name: "active cluster lowering its epoch regressed", cluster: prod("prod-east", 2), wantReason: store.ReasonClusterEpochRegressed, wantActive: prod("prod-east", 3)},
		{name: "another cluster at the active epoch is fenced", cluster: dr(3), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
		{name: "another cluster at a lower epoch is fenced", cluster: dr(1), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
		{name: "another cluster at a higher epoch takes over", cluster: dr(4), wantOutcome: store.DeclarationTakeover},
	}

	for _, step := range steps {
		res, err := s.DeclareClusterEpoch(ctx, step.cluster)
		if step.wantReason != "" {
			assertFenceRejected(t, err, step.wantReason, step.wantActive)
			continue
		}
		want := &store.DeclarationResult{Outcome: step.wantOutcome, Active: step.cluster}
		if diff := cmp.Diff(want, res); err != nil || diff != "" {
			t.Fatalf("%s: DeclareClusterEpoch() error = %v, result mismatch (-want +got):\n%s", step.name, err, diff)
		}
	}

	// Verify refresh preserves DeclaredTime while advancing LastSeenTime.
	var declaredBefore, lastSeenBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT DeclaredTime, LastSeenTime FROM ComponentOwnershipFence WHERE FenceId = 1`).Scan(&declaredBefore, &lastSeenBefore); err != nil {
		t.Fatalf("query fence timestamps before refresh: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.DeclareClusterEpoch(ctx, dr(4)); err != nil {
		t.Fatalf("DeclareClusterEpoch(refresh) error = %v", err)
	}
	var declaredAfter, lastSeenAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT DeclaredTime, LastSeenTime FROM ComponentOwnershipFence WHERE FenceId = 1`).Scan(&declaredAfter, &lastSeenAfter); err != nil {
		t.Fatalf("query fence timestamps after refresh: %v", err)
	}
	if !declaredAfter.Equal(declaredBefore) {
		t.Errorf("DeclaredTime changed on refresh: got %v, want %v", declaredAfter, declaredBefore)
	}
	if !lastSeenAfter.After(lastSeenBefore) {
		t.Errorf("LastSeenTime did not advance on refresh: before %v, after %v", lastSeenBefore, lastSeenAfter)
	}
}

func TestDeclareClusterEpoch_InvalidIdentity(t *testing.T) {
	ctx := context.Background()
	s, _ := setupTestStore(ctx, t)

	tests := []struct {
		name    string
		cluster store.ClusterIdentity
	}{
		{name: "invalid UUID", cluster: store.ClusterIdentity{UID: "not-a-uuid", Name: "prod", Epoch: 1}},
		{name: "empty name", cluster: store.ClusterIdentity{UID: prodClusterUID, Name: "", Epoch: 1}},
		{name: "zero epoch", cluster: store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.DeclareClusterEpoch(ctx, tt.cluster)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Errorf("DeclareClusterEpoch(%+v) code = %v, want %v (err: %v)", tt.cluster, got, codes.InvalidArgument, err)
			}
		})
	}
}

func TestDeclareClusterEpoch_TakeoverAndGauges(t *testing.T) {
	ctx := context.Background()
	s, pool := setupTestStore(ctx, t)

	// Before any declaration, gauges report FenceDeclared = false.
	g, err := s.ReadOwnershipGauges(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReadOwnershipGauges() before declaration error = %v", err)
	}
	if diff := cmp.Diff(store.OwnershipGauges{}, g); diff != "" {
		t.Errorf("ReadOwnershipGauges() before declaration mismatch (-want +got):\n%s", diff)
	}

	seedTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	insertComponentRow := `INSERT INTO Components (
  ComponentId, Name, ConfigProto, State, Generation, CreateTime, UpdateTime,
  SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime
) VALUES ($1, $2, $3, $4, 1, $5, $5, $6, $7, $8, $9, $10, $11)`

	// 1: Active owned component.
	if _, err := pool.Exec(ctx, insertComponentRow,
		int64(101), "fence-owned", []byte{}, int64(2), seedTime,
		"uid-prod", int64(3), "xmc", prodClusterUID, int64(4), seedTime,
	); err != nil {
		t.Fatalf("seed fence-owned: %v", err)
	}
	// 2: Active unowned component.
	if _, err := pool.Exec(ctx, insertComponentRow,
		int64(102), "fence-unowned", []byte{}, int64(2), seedTime,
		nil, nil, nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("seed fence-unowned: %v", err)
	}
	// 3: Deleted owned component (State = 5).
	if _, err := pool.Exec(ctx, insertComponentRow,
		int64(103), "fence-deleted", []byte{}, int64(5), seedTime,
		"uid-del", int64(1), "xmc", prodClusterUID, int64(4), seedTime,
	); err != nil {
		t.Fatalf("seed fence-deleted: %v", err)
	}

	prod := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 4}
	dr := store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 5}

	if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
		t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
	}

	res, err := s.DeclareClusterEpoch(ctx, dr)
	wantRes := &store.DeclarationResult{Outcome: store.DeclarationTakeover, Active: dr, RecordsReassigned: 1}
	if diff := cmp.Diff(wantRes, res); err != nil || diff != "" {
		t.Fatalf("DeclareClusterEpoch(dr) error = %v, result mismatch (-want +got):\n%s", err, diff)
	}

	// Verify reassigned row has SourceUid cleared, cluster/epoch set to dr, and SourceLastSeenTime == DeclaredTime.
	var uid pgtype.Text
	var clusterUID, namespace string
	var gen, epoch int64
	var lastSeen, declared time.Time
	if err := pool.QueryRow(ctx, `SELECT c.SourceUid, c.SourceGeneration, c.SourceNamespace, c.SourceClusterUid, c.SourceEpoch, c.SourceLastSeenTime, f.DeclaredTime
FROM Components c, ComponentOwnershipFence f
WHERE c.ComponentId = 101 AND f.FenceId = 1`).Scan(&uid, &gen, &namespace, &clusterUID, &epoch, &lastSeen, &declared); err != nil {
		t.Fatalf("query reassigned component: %v", err)
	}
	if uid.Valid || gen != 3 || namespace != "xmc" || clusterUID != drClusterUID || epoch != 5 {
		t.Errorf("reassigned owner = (uid=%v, gen=%d, ns=%q, cluster=%q, epoch=%d), want (NULL, 3, xmc, %s, 5)", uid, gen, namespace, clusterUID, epoch, drClusterUID)
	}
	if !lastSeen.Equal(declared) {
		t.Errorf("SourceLastSeenTime = %v, want takeover DeclaredTime %v", lastSeen, declared)
	}

	// Within the adoption grace period, StaleOwners is 0; past OwnerStaleThreshold, StaleOwners is 1.
	gNow, err := s.ReadOwnershipGauges(ctx, declared)
	if err != nil {
		t.Fatalf("ReadOwnershipGauges(now) error = %v", err)
	}
	if !gNow.FenceDeclared || gNow.FenceEpoch != 5 || gNow.StaleOwners != 0 || !gNow.FenceLastSeenTime.Equal(declared.UTC()) {
		t.Errorf("ReadOwnershipGauges(now) = %+v, want epoch 5, stale 0, lastSeen %v", gNow, declared.UTC())
	}

	gAfterGrace, err := s.ReadOwnershipGauges(ctx, declared.Add(ownership.OwnerStaleThreshold+time.Minute))
	if err != nil {
		t.Fatalf("ReadOwnershipGauges(after grace) error = %v", err)
	}
	if gAfterGrace.StaleOwners != 1 {
		t.Errorf("ReadOwnershipGauges(after grace) StaleOwners = %d, want 1", gAfterGrace.StaleOwners)
	}
}

func TestPostgresStore_OwnershipFenceSchema(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		args    []any
		wantErr bool
	}{
		{name: "epoch 0 violates the CHECK", args: []any{int64(1), prodClusterUID, "prod", int64(0), now, now}, wantErr: true},
		{name: "NULL LastSeenTime is rejected", args: []any{int64(1), prodClusterUID, "prod", int64(1), now, nil}, wantErr: true},
		{name: "FenceId other than 1 is allowed", args: []any{int64(2), prodClusterUID, "prod", int64(1), now, now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, pool := setupTestStore(ctx, t)
			_, err := pool.Exec(ctx, `INSERT INTO ComponentOwnershipFence (FenceId, ActiveClusterUid, ActiveClusterName, Epoch, DeclaredTime, LastSeenTime) VALUES ($1, $2, $3, $4, $5, $6)`, tt.args...)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("Exec(%v) error = %v, wantErr %t", tt.args, err, tt.wantErr)
			}
		})
	}
}

func assertFenceRejected(t *testing.T, err error, wantReason string, active store.ClusterIdentity) {
	t.Helper()
	st := status.Convert(err)
	want := []any{&errdetails.ErrorInfo{
		Reason: wantReason,
		Domain: store.ErrorInfoDomain,
		Metadata: map[string]string{
			"operation":           "DeclareClusterEpoch",
			"active_cluster_uid":  active.UID,
			"active_cluster_name": active.Name,
			"active_epoch":        fmt.Sprint(active.Epoch),
		},
	}}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("DeclareClusterEpoch() code = %v, want %v (error = %v)", st.Code(), codes.FailedPrecondition, err)
	}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("DeclareClusterEpoch() details mismatch (-want +got):\n%s", diff)
	}
}
