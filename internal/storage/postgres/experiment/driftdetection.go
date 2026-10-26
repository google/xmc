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

package experiment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// driftRollbackTimeout bounds ending the read-only transaction of FetchDriftedComponents once
// its reads are done, even if the caller's context is already cancelled.
const driftRollbackTimeout = 5 * time.Second

var (
	// selectExperimentConsistencySQL reads the consistency flag of one active experiment from the
	// ExperimentConsistencyInfo view, which has no row for inactive or missing experiments.
	selectExperimentConsistencySQL = safesql.New(`SELECT IsExperimentConsistent
FROM ExperimentConsistencyInfo
WHERE Xid = $1`)

	// selectDriftedComponentsSQL lists the components of one experiment's snapshot that are
	// missing or deleted from the catalog, or whose observed generation differs from the locked
	// generation, by name. ComponentId breaks ties between equal names deterministically.
	selectDriftedComponentsSQL = safesql.New(`SELECT c.Name, s.LockedGeneration, c.ObservedGeneration, c.State, s.ComponentId
FROM ExperimentComponentSnapshots s
LEFT JOIN Components c ON s.ComponentId = c.ComponentId
WHERE s.Xid = $1
  AND (c.ComponentId IS NULL OR c.State = $2 OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration))
ORDER BY c.Name ASC, s.ComponentId ASC`)

	// selectDriftedComponentsLimitSQL is selectDriftedComponentsSQL capped at $3 rows.
	selectDriftedComponentsLimitSQL = safesql.Concat(selectDriftedComponentsSQL, safesql.New(`
LIMIT $3`))
)

// FetchDriftedComponents checks the ExperimentConsistencyInfo view and, if the experiment has
// drifted, queries the drifted component generation comparisons. Both reads run in a single
// REPEATABLE READ, read-only transaction, the counterpart of the Spanner read-only transaction,
// so they see one consistent snapshot. It returns nil for consistent, inactive, snapshot-free
// or missing experiments. A limit <= 0 returns all drifted components.
func (r *Repository) FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	if r.pool == nil {
		return nil, errNilPool
	}
	txn, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("failed to begin read-only transaction for xid %d: %w", xid, pgdb.TranslateError(err, experimentEntity(xid)))
	}
	defer func() {
		// The transaction only read, so ending it by rollback loses nothing; a background
		// context lets the connection return to the pool cleanly even if ctx is cancelled.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), driftRollbackTimeout)
		defer cancel()
		_ = txn.Rollback(rollbackCtx)
	}()

	isConsistent, err := checkConsistency(ctx, txn, xid)
	if err != nil || isConsistent {
		return nil, err
	}
	return fetchDriftedComponents(ctx, txn, xid, limit)
}

// checkConsistency reports whether the experiment xid is consistent with the component catalog.
// Experiments without a row in the ExperimentConsistencyInfo view (inactive or not found) are
// treated as consistent.
func checkConsistency(ctx context.Context, db pgdb.DBTX, xid int64) (bool, error) {
	var isConsistent pgtype.Bool
	err := db.QueryRow(ctx, selectExperimentConsistencySQL.String(), xid).Scan(&isConsistent)
	if errors.Is(err, pgx.ErrNoRows) {
		// Experiment is inactive or not found in active view; treat as consistent.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("querying consistency view failed for xid %d: %w", xid, pgdb.TranslateError(err, experimentEntity(xid)))
	}
	if !isConsistent.Valid {
		return true, nil
	}
	return isConsistent.Bool, nil
}

// fetchDriftedComponents lists up to limit (all when limit <= 0) drifted components of xid,
// ordered by name.
func fetchDriftedComponents(ctx context.Context, db pgdb.DBTX, xid int64, limit int) ([]storage.ComponentDiff, error) {
	query := selectDriftedComponentsSQL
	args := []any{xid, int64(storage.ComponentStateDeleted)}
	if limit > 0 {
		query = selectDriftedComponentsLimitSQL
		args = append(args, int64(limit))
	}
	rows, err := db.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("reading drifted component rows failed: %w", pgdb.TranslateError(err, experimentEntity(xid)))
	}
	defer rows.Close()

	var diffs []storage.ComponentDiff
	if limit > 0 {
		diffs = make([]storage.ComponentDiff, 0, limit)
	}
	for rows.Next() {
		diff, err := scanComponentDiff(rows)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading drifted component row failed: %w", pgdb.TranslateError(err, experimentEntity(xid)))
	}
	return diffs, nil
}

// scanComponentDiff parses a single drifted component row into a backend-neutral ComponentDiff.
func scanComponentDiff(row pgx.Row) (storage.ComponentDiff, error) {
	var (
		name               pgtype.Text
		lockedGeneration   int64
		observedGeneration pgtype.Int8
		state              pgtype.Int8
		componentID        int64
	)
	if err := row.Scan(&name, &lockedGeneration, &observedGeneration, &state, &componentID); err != nil {
		return storage.ComponentDiff{}, fmt.Errorf("failed to scan component diff row: %w", err)
	}
	return storage.ComponentDiff{
		ComponentID:        componentID,
		Name:               nullStringPtr(name),
		LockedGeneration:   lockedGeneration,
		ObservedGeneration: nullInt64Ptr(observedGeneration),
		State:              nullInt64Ptr(state),
	}, nil
}

// nullStringPtr maps a NULL text to nil.
func nullStringPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

// nullInt64Ptr maps a NULL integer to nil.
func nullInt64Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
