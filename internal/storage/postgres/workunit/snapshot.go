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

package workunit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// snapshotRollbackTimeout bounds ending a snapshot read transaction once its reads are done.
const snapshotRollbackTimeout = 5 * time.Second

// snapshotReadTxOptions start a REPEATABLE READ READ ONLY transaction, in which every statement
// sees the same database snapshot.
var snapshotReadTxOptions = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// readSnapshot runs fn inside a REPEATABLE READ READ ONLY transaction on the repository's pool, so
// that reads spanning several statements observe one consistent database snapshot, the way the
// Spanner implementation uses a read-only transaction. The transaction ends when fn returns.
func (r *Repository) readSnapshot(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if r == nil || r.pool == nil {
		return errNilPool
	}
	tx, err := r.pool.BeginTx(ctx, snapshotReadTxOptions)
	if err != nil {
		return fmt.Errorf("failed to begin read-only postgres transaction: %w", err)
	}
	defer func() {
		// A read-only transaction has nothing to commit; rolling it back merely releases the
		// snapshot and the connection, even after ctx has been canceled.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	return fn(ctx, tx)
}
