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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// UIDs of the kube-system Namespaces of the prod and DR test clusters.
const (
	prodClusterUID = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a01"
	drClusterUID   = "7d3e2a10-8c4b-4f6e-b1a2-3c4d5e6f7a02"
)

// TestDeclareClusterEpoch_Parity runs one declaration sequence against MemoryStore and SpannerStore:
// the first owner keeps the fence, the active cluster refreshes (possibly renamed) or raises its
// epoch, and only a strictly higher epoch from another cluster takes the fence over.
func TestDeclareClusterEpoch_Parity(t *testing.T) {
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
		wantActive  store.ClusterIdentity // Fence holder named by the rejection metadata.
	}{
		{name: "first declaration creates the fence", cluster: prod("prod", 1), wantOutcome: store.DeclarationCreated},
		{name: "active cluster refreshes its epoch", cluster: prod("prod", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster refreshes under a new name", cluster: prod("prod-east", 1), wantOutcome: store.DeclarationRefreshed},
		{name: "active cluster raises its epoch", cluster: prod("prod-east", 3), wantOutcome: store.DeclarationRaised},
		{name: "active cluster lowering its epoch regressed", cluster: prod("prod-east", 2), wantReason: store.ReasonClusterEpochRegressed, wantActive: prod("prod-east", 3)},
		{name: "another cluster at the active epoch is fenced", cluster: dr(3), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
		{name: "another cluster at a lower epoch is fenced", cluster: dr(1), wantReason: store.ReasonClusterFenced, wantActive: prod("prod-east", 3)},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Steps are intentionally sequential because each one depends on the fence left by the last.
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
		})
	}
}

// TestDeclareClusterEpoch_TakeoverParity checks on both backends that a takeover moves every active
// owned component to the new cluster without its owner UID, leaves unowned and deleted components
// alone, and that the new cluster's next intent in the same namespace adopts the component under
// the same ComponentId (R2), while the old primary is fenced.
func TestDeclareClusterEpoch_TakeoverParity(t *testing.T) {
	prod := store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 4}
	dr := store.ClusterIdentity{UID: drClusterUID, Name: "dr", Epoch: 5}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			owned := seedOwnedComponent(ctx, t, s, "fence-owned", false)
			seedOwnedComponent(ctx, t, s, "fence-legacy", true)
			seedOwnedComponent(ctx, t, s, "fence-deleted", false)
			if err := s.ExecuteDeletion(ctx, "fence-deleted", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
				t.Fatalf("ExecuteDeletion() error = %v", err)
			}
			if _, err := s.DeclareClusterEpoch(ctx, prod); err != nil {
				t.Fatalf("DeclareClusterEpoch(prod) error = %v", err)
			}

			res, err := s.DeclareClusterEpoch(ctx, dr)
			if diff := cmp.Diff(&store.DeclarationResult{Outcome: store.DeclarationTakeover, Active: dr, RecordsReassigned: 1}, res); err != nil || diff != "" {
				t.Fatalf("DeclareClusterEpoch(dr) error = %v, result mismatch (-want +got):\n%s", err, diff)
			}
			assertStoredOwner(ctx, t, s, "fence-owned", &store.SourceOwner{Generation: 3, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 5})
			assertStoredOwner(ctx, t, s, "fence-legacy", nil)
			_, err = s.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: prodClusterUID, Name: "prod", Epoch: 5})
			assertFenceRejected(t, err, store.ReasonClusterFenced, dr)

			drSource := store.RequestSource{UID: "uid-dr", Generation: 1, Namespace: "xmc", ClusterUID: drClusterUID, Epoch: 5}
			adopted, err := s.SetSourcedComponentIntent(ctx, "fence-owned", intentSpec("v1.0.0"), nil, drSource)
			if err != nil {
				t.Fatalf("SetSourcedComponentIntent() after takeover error = %v", err)
			}
			if adopted.ComponentID != owned.ComponentID || adopted.Generation != owned.Generation {
				t.Errorf("adopted ComponentID %d, generation %d; want the seeded %d, %d", adopted.ComponentID, adopted.Generation, owned.ComponentID, owned.Generation)
			}
			assertStoredOwner(ctx, t, s, "fence-owned", ownerFor(drSource))
		})
	}
}

// assertFenceRejected checks that err is a FailedPrecondition with an ErrorInfo wantReason naming
// active as the fence holder.
func assertFenceRejected(t *testing.T, err error, wantReason string, active store.ClusterIdentity) {
	t.Helper()
	st := status.Convert(err)
	want := []any{&errdetails.ErrorInfo{
		Reason:   wantReason,
		Domain:   store.ErrorInfoDomain,
		Metadata: map[string]string{"operation": "DeclareClusterEpoch", "active_cluster_name": active.Name, "active_epoch": fmt.Sprint(active.Epoch)},
	}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); st.Code() != codes.FailedPrecondition || diff != "" {
		t.Errorf("DeclareClusterEpoch() error = %v, details mismatch (-want +got):\n%s", err, diff)
	}
}

// assertStoredOwner checks the recorded owner of name.
func assertStoredOwner(ctx context.Context, t *testing.T, s store.Store, name string, want *store.SourceOwner) {
	t.Helper()
	rec, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(want, rec.Owner); diff != "" {
		t.Errorf("Owner of %s mismatch (-want +got):\n%s", name, diff)
	}
}
