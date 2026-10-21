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

package spanner

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"

	"xmanager_cloud/pkg/components/store"
)

const (
	fenceTable = "ComponentOwnershipFence"
	fenceID    = int64(1)
	// takeOverOwnersSQL moves every active owned component to the declaring cluster and clears its
	// owner UID so that the cluster's next intent adopts it (R2). SourceLastSeenTime is set to the
	// commit time, which starts the adoption grace period of the owner_stale gauge.
	takeOverOwnersSQL = `UPDATE Components SET SourceClusterUid = @clusterUid, SourceEpoch = @epoch, SourceUid = NULL, SourceLastSeenTime = PENDING_COMMIT_TIMESTAMP() WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL`

	// ownershipGaugesSQL reads the fence and counts active owned components whose last-seen
	// timestamp is at or before @cutoff in one snapshot. Records reassigned by a takeover count
	// only once the adoption grace period that starts at the takeover commit has ended.
	ownershipGaugesSQL = `SELECT
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch,
  (SELECT f.LastSeenTime FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceLastSeenTime,
  (SELECT COUNT(*) FROM Components WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL AND SourceLastSeenTime <= @cutoff) AS StaleOwners`

	// fenceColumnsSQL adds the fence holder to a query as scalar subqueries, NULL before the first
	// declaration, so that one single-use read sees the fence and the component in one snapshot.
	fenceColumnsSQL = `(SELECT f.ActiveClusterUid FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterUid,
  (SELECT f.ActiveClusterName FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterName,
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch`
)

// DeclareClusterEpoch declares cluster the writer of sourced intents within Spanner, reading the
// fence, reassigning owners on a takeover, and writing the fence in one read-write transaction.
func (s *SpannerStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	if err := store.ValidateClusterIdentity(cluster); err != nil {
		return nil, err
	}
	var res *store.DeclarationResult
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		res, err = declareClusterEpochTx(ctx, txn, cluster)
		return err
	})
	if err != nil {
		return nil, store.RecordSourceConflictError(err)
	}
	return res, nil
}

var _ store.OwnershipGaugeReader = (*SpannerStore)(nil)

// ReadOwnershipGauges reads the fence and counts the stale owners in Spanner at now with one
// single-use read, so the fence and the count come from one snapshot.
func (s *SpannerStore) ReadOwnershipGauges(ctx context.Context, now time.Time) (store.OwnershipGauges, error) {
	stmt := spanner.Statement{SQL: ownershipGaugesSQL, Params: map[string]any{"cutoff": now.Add(-store.OwnerStaleThreshold)}}
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		return store.OwnershipGauges{}, fmt.Errorf("failed to read the ownership gauges: %w", err)
	}
	var epoch spanner.NullInt64
	var lastSeen spanner.NullTime
	var g store.OwnershipGauges
	if err := row.Columns(&epoch, &lastSeen, &g.StaleOwners); err != nil {
		return store.OwnershipGauges{}, fmt.Errorf("failed to decode the ownership gauges: %w", err)
	}
	g.FenceDeclared, g.FenceEpoch, g.FenceLastSeenTime = epoch.Valid, epoch.Int64, lastSeen.Time
	return g, nil
}

// declareClusterEpochTx applies a declaration by cluster within txn.
func declareClusterEpochTx(ctx context.Context, txn *spanner.ReadWriteTransaction, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	fence, err := readFence(ctx, txn)
	if err != nil {
		return nil, err
	}
	outcome, err := store.PlanDeclaration(fence, cluster)
	if err != nil {
		return nil, err
	}
	res := &store.DeclarationResult{Outcome: outcome, Active: cluster}
	if outcome == store.DeclarationTakeover {
		stmt := spanner.Statement{SQL: takeOverOwnersSQL, Params: map[string]any{"clusterUid": cluster.UID, "epoch": cluster.Epoch}}
		if res.RecordsReassigned, err = txn.Update(ctx, stmt); err != nil {
			return nil, fmt.Errorf("failed to reassign component owners: %w", err)
		}
	}
	return res, txn.BufferWrite([]*spanner.Mutation{fenceMutation(fence, cluster, outcome)})
}

// fenceMutation returns the write of an accepted declaration by cluster over fence. A refresh
// updates only LastSeenTime (and any changed name) so it takes no write lock on Epoch or UID.
func fenceMutation(fence *store.ClusterIdentity, cluster store.ClusterIdentity, outcome store.DeclarationOutcome) *spanner.Mutation {
	if outcome == store.DeclarationRefreshed {
		cols := map[string]any{"FenceId": fenceID, "LastSeenTime": spanner.CommitTimestamp}
		if fence.Name != cluster.Name {
			cols["ActiveClusterName"] = cluster.Name
		}
		return spanner.UpdateMap(fenceTable, cols)
	}
	return spanner.InsertOrUpdateMap(fenceTable, map[string]any{
		"FenceId": fenceID, "ActiveClusterUid": cluster.UID, "ActiveClusterName": cluster.Name,
		"Epoch": cluster.Epoch, "DeclaredTime": spanner.CommitTimestamp, "LastSeenTime": spanner.CommitTimestamp,
	})
}

// checkFence reads the fence within txn and returns the rejection of src by CheckClusterFence.
// Unsourced requests (nil src) skip the read.
func checkFence(ctx context.Context, txn *spanner.ReadWriteTransaction, operation string, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	fence, err := readFence(ctx, txn)
	if err != nil {
		return err
	}
	return store.CheckClusterFence(operation, fence, src)
}

// readFence returns the current fence holder, or nil before the first declaration.
func readFence(ctx context.Context, txn *spanner.ReadWriteTransaction) (*store.ClusterIdentity, error) {
	row, err := txn.ReadRow(ctx, fenceTable, spanner.Key{fenceID}, []string{"ActiveClusterUid", "ActiveClusterName", "Epoch"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the ownership fence: %w", err)
	}
	var fence store.ClusterIdentity
	if err := row.Columns(&fence.UID, &fence.Name, &fence.Epoch); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence: %w", err)
	}
	return &fence, nil
}

// scanFenceColumns returns the fence holder from the columns that fenceColumnsSQL adds to a
// query, or nil before the first declaration (when the scalar subqueries are NULL).
func scanFenceColumns(row *spanner.Row) (*store.ClusterIdentity, error) {
	var uid, name spanner.NullString
	var epoch spanner.NullInt64
	if err := row.ColumnByName("FenceClusterUid", &uid); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceClusterUid: %w", err)
	}
	if err := row.ColumnByName("FenceClusterName", &name); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceClusterName: %w", err)
	}
	if err := row.ColumnByName("FenceEpoch", &epoch); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceEpoch: %w", err)
	}
	if !uid.Valid {
		return nil, nil
	}
	return &store.ClusterIdentity{UID: uid.StringVal, Name: name.StringVal, Epoch: epoch.Int64}, nil
}
