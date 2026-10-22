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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/ownership"
)

const (
	fenceID = int64(1)

	maxTxAttempts     = 5
	initialTxBackoff  = 10 * time.Millisecond
	maxTxBackoff      = 200 * time.Millisecond
	txRollbackTimeout = 5 * time.Second

	// takeOverOwnersSQL moves every active owned component to the declaring cluster and clears its
	// owner UID so that the cluster's next intent adopts it (R2). SourceLastSeenTime is set to the
	// transaction commit timestamp, which starts the adoption grace period of the owner_stale gauge.
	takeOverOwnersSQL = `UPDATE Components
SET SourceClusterUid = $1, SourceEpoch = $2, SourceUid = NULL, SourceLastSeenTime = $3
WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL`

	// ownershipGaugesSQL reads the fence and counts active owned components whose last-seen
	// timestamp is at or before $1 in one snapshot. Records reassigned by a takeover count
	// only once the adoption grace period that starts at the takeover commit has ended.
	ownershipGaugesSQL = `SELECT
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch,
  (SELECT f.LastSeenTime FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceLastSeenTime,
  (SELECT COUNT(*) FROM Components WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL AND SourceLastSeenTime <= $1) AS StaleOwners`

	selectFenceSQL = `SELECT ActiveClusterUid, ActiveClusterName, Epoch
FROM ComponentOwnershipFence
WHERE FenceId = $1`

	refreshFenceSQL = `UPDATE ComponentOwnershipFence
SET ActiveClusterName = $2, LastSeenTime = $3
WHERE FenceId = $1`

	upsertFenceSQL = `INSERT INTO ComponentOwnershipFence (FenceId, ActiveClusterUid, ActiveClusterName, Epoch, DeclaredTime, LastSeenTime)
VALUES ($1, $2, $3, $4, $5, $5)
ON CONFLICT (FenceId) DO UPDATE SET
  ActiveClusterUid = EXCLUDED.ActiveClusterUid,
  ActiveClusterName = EXCLUDED.ActiveClusterName,
  Epoch = EXCLUDED.Epoch,
  DeclaredTime = EXCLUDED.DeclaredTime,
  LastSeenTime = EXCLUDED.LastSeenTime`

	// fenceColumnsSQL adds the fence holder to a query as scalar subqueries, NULL before the first
	// declaration, so that one single-query read sees the fence and the component in one snapshot.
	fenceColumnsSQL = `(SELECT f.ActiveClusterUid FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterUid,
  (SELECT f.ActiveClusterName FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterName,
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch`
)

// Ensure PostgresStore implements OwnershipGaugeReader at compile time.
var _ store.OwnershipGaugeReader = (*PostgresStore)(nil)

// DeclareClusterEpoch declares cluster the writer of sourced intents within PostgreSQL, reading the
// fence, reassigning owners on a takeover, and writing the fence in one serializable transaction.
func (s *PostgresStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	if err := ownership.ValidateClusterIdentity(cluster); err != nil {
		return nil, err
	}
	var res *store.DeclarationResult
	_, err := s.runInTx(ctx, func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error {
		var err error
		res, err = declareClusterEpochTx(ctx, tx, cluster, commitTime)
		return err
	})
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	return res, nil
}

// ReadOwnershipGauges reads the fence and counts the stale owners in PostgreSQL at now with one
// single-query read, so the fence and the count come from one snapshot.
func (s *PostgresStore) ReadOwnershipGauges(ctx context.Context, now time.Time) (store.OwnershipGauges, error) {
	cutoff := now.Add(-ownership.OwnerStaleThreshold)
	var epoch pgtype.Int8
	var lastSeen pgtype.Timestamptz
	var g store.OwnershipGauges
	if err := s.pool.QueryRow(ctx, ownershipGaugesSQL, cutoff).Scan(&epoch, &lastSeen, &g.StaleOwners); err != nil {
		return store.OwnershipGauges{}, fmt.Errorf("failed to read the ownership gauges: %w", err)
	}
	g.FenceDeclared = epoch.Valid
	g.FenceEpoch = epoch.Int64
	if lastSeen.Valid {
		g.FenceLastSeenTime = lastSeen.Time.UTC()
	}
	return g, nil
}

// declareClusterEpochTx applies a declaration by cluster within tx.
func declareClusterEpochTx(ctx context.Context, tx pgx.Tx, cluster store.ClusterIdentity, commitTime time.Time) (*store.DeclarationResult, error) {
	fence, err := readFence(ctx, tx)
	if err != nil {
		return nil, err
	}
	outcome, err := ownership.PlanDeclaration(fence, cluster)
	if err != nil {
		return nil, err
	}
	res := &store.DeclarationResult{Outcome: outcome, Active: cluster}
	if outcome == store.DeclarationTakeover {
		tag, err := tx.Exec(ctx, takeOverOwnersSQL, cluster.UID, cluster.Epoch, commitTime)
		if err != nil {
			return nil, fmt.Errorf("failed to reassign component owners: %w", err)
		}
		res.RecordsReassigned = tag.RowsAffected()
	}
	return res, writeFenceTx(ctx, tx, cluster, outcome, commitTime)
}

// writeFenceTx writes an accepted declaration by cluster. A refresh updates only LastSeenTime and
// ActiveClusterName so DeclaredTime is preserved.
func writeFenceTx(ctx context.Context, tx pgx.Tx, cluster store.ClusterIdentity, outcome store.DeclarationOutcome, commitTime time.Time) error {
	if outcome == store.DeclarationRefreshed {
		_, err := tx.Exec(ctx, refreshFenceSQL, fenceID, cluster.Name, commitTime)
		return err
	}
	_, err := tx.Exec(ctx, upsertFenceSQL, fenceID, cluster.UID, cluster.Name, cluster.Epoch, commitTime)
	return err
}

// checkFence reads the fence within q and returns the rejection of src by CheckClusterFence.
// Unsourced requests (nil src) skip the read.
func checkFence(ctx context.Context, q pgdb.DBTX, operation string, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	fence, err := readFence(ctx, q)
	if err != nil {
		return err
	}
	return ownership.CheckClusterFence(operation, fence, src)
}

// readFence returns the current fence holder, or nil before the first declaration.
func readFence(ctx context.Context, q pgdb.DBTX) (*store.ClusterIdentity, error) {
	var fence store.ClusterIdentity
	err := q.QueryRow(ctx, selectFenceSQL, fenceID).Scan(&fence.UID, &fence.Name, &fence.Epoch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the ownership fence: %w", err)
	}
	return &fence, nil
}

// runInTx runs fn inside a serializable read-write transaction with automatic retry on transient
// serialization, deadlock, or concurrent unique-violation conflicts.
func (s *PostgresStore) runInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error) (time.Time, error) {
	backoff := initialTxBackoff
	var lastErr error
	for attempt := 1; attempt <= maxTxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		commitTime := time.Now().UTC().Truncate(time.Microsecond)
		lastErr = s.runTxAttempt(ctx, commitTime, fn)
		if lastErr == nil {
			return commitTime, nil
		}
		if !pgdb.IsRetryableTxError(lastErr) && !pgdb.IsUniqueViolation(lastErr) {
			return time.Time{}, lastErr
		}
		if attempt < maxTxAttempts {
			if err := waitBackoff(ctx, backoff); err != nil {
				return time.Time{}, err
			}
			backoff = min(backoff*2, maxTxBackoff)
		}
	}
	return time.Time{}, pgdb.TranslateError(lastErr, "transaction")
}

func (s *PostgresStore) runTxAttempt(ctx context.Context, commitTime time.Time, fn func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("failed to begin postgres transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), txRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := fn(ctx, tx, commitTime); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit postgres transaction: %w", err)
	}
	committed = true
	return nil
}

func waitBackoff(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
