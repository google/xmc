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

package store

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
)

const (
	fenceTable = "ComponentOwnershipFence"
	fenceID    = int64(1)
	// takeOverOwnersSQL moves every active owned component to the declaring cluster and clears its
	// owner UID so that the cluster's next intent adopts it (R2). SourceLastSeenTime is set to the
	// commit time, which starts the adoption grace period of the owner_stale gauge.
	takeOverOwnersSQL = `UPDATE Components SET SourceClusterUid = @clusterUid, SourceEpoch = @epoch, SourceUid = NULL, SourceLastSeenTime = PENDING_COMMIT_TIMESTAMP() WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL`

	// countStaleOwnersSQL counts active owned components whose owner UID was cleared by a takeover
	// or whose last-seen timestamp is at or before @cutoff.
	countStaleOwnersSQL = `SELECT COUNT(*) FROM Components WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL AND (SourceUid IS NULL OR SourceLastSeenTime <= @cutoff)`

	// fenceColumnsSQL adds the fence holder to a query as scalar subqueries, NULL before the first
	// declaration, so that one single-use read sees the fence and the component in one snapshot.
	fenceColumnsSQL = `(SELECT f.ActiveClusterUid FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterUid,
  (SELECT f.ActiveClusterName FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterName,
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch`
)

// DeclareClusterEpoch declares cluster the writer of sourced intents within Spanner, reading the
// fence, reassigning owners on a takeover, and writing the fence in one read-write transaction.
func (s *SpannerStore) DeclareClusterEpoch(ctx context.Context, cluster ClusterIdentity) (*DeclarationResult, error) {
	if err := ValidateClusterIdentity(cluster); err != nil {
		return nil, err
	}
	var res *DeclarationResult
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		res, err = declareClusterEpochTx(ctx, txn, cluster)
		return err
	})
	if err != nil {
		return nil, recordSourceConflictError(err)
	}
	stale, staleErr := queryStaleOwnerCount(ctx, s.client.Single(), time.Now().UTC())
	recordClusterDeclarationMetrics(res.Active.Epoch, stale, staleErr == nil)
	return res, nil
}

// queryStaleOwnerCount counts active owned components with an unassigned or stale owner at now.
func queryStaleOwnerCount(ctx context.Context, queryable spannerQueryable, now time.Time) (int64, error) {
	stmt := spanner.Statement{SQL: countStaleOwnersSQL, Params: map[string]any{"cutoff": now.Add(-OwnerStaleThreshold)}}
	iter := queryable.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		return 0, err
	}
	var count int64
	if err := row.Columns(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// declareClusterEpochTx applies a declaration by cluster within txn.
func declareClusterEpochTx(ctx context.Context, txn *spanner.ReadWriteTransaction, cluster ClusterIdentity) (*DeclarationResult, error) {
	fence, err := readFence(ctx, txn)
	if err != nil {
		return nil, err
	}
	outcome, err := planDeclaration(fence, cluster)
	if err != nil {
		return nil, err
	}
	res := &DeclarationResult{Outcome: outcome, Active: cluster}
	if outcome == DeclarationTakeover {
		stmt := spanner.Statement{SQL: takeOverOwnersSQL, Params: map[string]any{"clusterUid": cluster.UID, "epoch": cluster.Epoch}}
		if res.RecordsReassigned, err = txn.Update(ctx, stmt); err != nil {
			return nil, fmt.Errorf("failed to reassign component owners: %w", err)
		}
	}
	return res, txn.BufferWrite([]*spanner.Mutation{fenceMutation(fence, cluster, outcome)})
}

// fenceMutation returns the write of an accepted declaration by cluster over fence. A refresh
// updates only LastSeenTime (and any changed name) so it takes no write lock on Epoch or UID.
func fenceMutation(fence *ClusterIdentity, cluster ClusterIdentity, outcome DeclarationOutcome) *spanner.Mutation {
	if outcome == DeclarationRefreshed {
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

// checkFence reads the fence within txn and returns the rejection of src by checkClusterFence.
// Unsourced requests (nil src) skip the read.
func checkFence(ctx context.Context, txn *spanner.ReadWriteTransaction, operation string, src *RequestSource) error {
	if src == nil {
		return nil
	}
	fence, err := readFence(ctx, txn)
	if err != nil {
		return err
	}
	return checkClusterFence(operation, fence, src)
}

// readFence returns the current fence holder, or nil before the first declaration.
func readFence(ctx context.Context, txn *spanner.ReadWriteTransaction) (*ClusterIdentity, error) {
	row, err := txn.ReadRow(ctx, fenceTable, spanner.Key{fenceID}, []string{"ActiveClusterUid", "ActiveClusterName", "Epoch"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the ownership fence: %w", err)
	}
	var fence ClusterIdentity
	if err := row.Columns(&fence.UID, &fence.Name, &fence.Epoch); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence: %w", err)
	}
	return &fence, nil
}

// scanFenceColumns returns the fence holder from the columns that fenceColumnsSQL adds to a
// query, or nil before the first declaration (when the scalar subqueries are NULL).
func scanFenceColumns(row *spanner.Row) (*ClusterIdentity, error) {
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
	return &ClusterIdentity{UID: uid.StringVal, Name: name.StringVal, Epoch: epoch.Int64}, nil
}
