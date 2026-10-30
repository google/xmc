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

// Package retention deletes expired PostgreSQL rows. PostgreSQL has no counterpart to the
// Cloud Spanner ROW DELETION POLICY clauses on StatusMessages, WorkUnitStatusHistory, PausedTasks
// and CliLoginCodes, so a periodic caller must delete those rows explicitly.
package retention

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

const entityName = "expired rows"

// sweepLockKey is the pg_try_advisory_xact_lock key that serializes DeleteExpiredRows calls
// across all ESS replicas sharing a database. It is "xmcttl" in ASCII; no other code in the
// database takes this advisory lock.
const sweepLockKey int64 = 0x786d6374746c

// tryLockSQL takes the sweep lock until the current transaction ends, without waiting for it.
var tryLockSQL = safesql.New(`SELECT pg_try_advisory_xact_lock($1)`)

// deleteExpiredSQLs delete up to $1 expired rows from one table each, mirroring the Spanner
// ROW DELETION POLICY of that table. now() is the start time of the transaction. Rows whose
// ExpirationTime is NULL (pending PausedTasks) never match. Rows are selected by primary key.
var deleteExpiredSQLs = []safesql.TrustedSQLString{
	// OLDER_THAN(ExpirationTime, INTERVAL 0 DAY): 30 days, or 180 days for ERROR and FATAL.
	safesql.New(`DELETE FROM StatusMessages
WHERE (Xid, CreateTime, MessageId) IN (
  SELECT Xid, CreateTime, MessageId FROM StatusMessages WHERE ExpirationTime < now() LIMIT $1)`),
	// OLDER_THAN(ExpirationTime, INTERVAL 0 DAY): 30 days.
	safesql.New(`DELETE FROM WorkUnitStatusHistory
WHERE (Xid, WorkUnitId, HistoryId) IN (
  SELECT Xid, WorkUnitId, HistoryId FROM WorkUnitStatusHistory WHERE ExpirationTime < now() LIMIT $1)`),
	// OLDER_THAN(ExpirationTime, INTERVAL 0 DAY): 7 days after ProcessedTime.
	safesql.New(`DELETE FROM PausedTasks
WHERE (Xid, WorkUnitId, TaskId) IN (
  SELECT Xid, WorkUnitId, TaskId FROM PausedTasks WHERE ExpirationTime < now() LIMIT $1)`),
	// OLDER_THAN(ExpiresAt, INTERVAL 1 DAY).
	safesql.New(`DELETE FROM CliLoginCodes
WHERE CodeHash IN (
  SELECT CodeHash FROM CliLoginCodes WHERE ExpiresAt < now() - INTERVAL '1 day' LIMIT $1)`),
}

var _ storage.ExpiredRowStore = (*Repository)(nil)

// Repository deletes expired rows from the ESS database in its own read-write transactions.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a PostgreSQL expired-row Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// DeleteExpiredRows deletes up to batchSize expired rows from each table in one transaction that
// holds the sweep advisory lock, and returns the total number of rows deleted. When another
// transaction holds the lock, it deletes nothing and returns 0.
func (r *Repository) DeleteExpiredRows(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, pgdb.WrapStatusError(fmt.Sprintf("batch size must be positive, got %d", batchSize), grpcerr.ErrInvalidArgument, nil)
	}
	var deleted int64
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// pgdb.RunInTx may re-run this callback, so reset the count on every attempt.
		deleted = 0
		locked, err := tryLock(ctx, tx)
		if err != nil || !locked {
			return err
		}
		for _, stmt := range deleteExpiredSQLs {
			tag, err := tx.Exec(ctx, stmt.String(), batchSize)
			if err != nil {
				return pgdb.TranslateError(err, entityName)
			}
			deleted += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// tryLock takes the sweep lock for the rest of tx and reports whether it was free.
func tryLock(ctx context.Context, tx pgx.Tx) (bool, error) {
	var locked bool
	if err := tx.QueryRow(ctx, tryLockSQL.String(), sweepLockKey).Scan(&locked); err != nil {
		return false, pgdb.TranslateError(err, entityName)
	}
	return locked, nil
}
