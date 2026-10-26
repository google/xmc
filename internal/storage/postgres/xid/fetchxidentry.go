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

package xid

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// selectXidSQL reads one Xids row by its sequential ID without locking it. Its columns are
// xidschema.FetchedXIDColumns in order.
var selectXidSQL = safesql.New(`SELECT Xid, ParentXid, Creator, OriginatingSystem, Acls, CreateTime, UpdateTime
FROM Xids
WHERE Xid = $1`)

// lockXidSQL reads one Xids row and locks it until the transaction ends. RunInTransaction
// callbacks run at READ COMMITTED, where a plain SELECT takes no row lock and concurrent
// read-modify-write callers would silently overwrite each other's updates; the lock serializes
// them. FOR NO KEY UPDATE, unlike FOR UPDATE, does not conflict with the FOR KEY SHARE locks that
// foreign-key checks take.
var lockXidSQL = safesql.Concat(selectXidSQL, safesql.New(`
FOR NO KEY UPDATE`))

// updateXidSQL overwrites the columns that an XidEntry update may change, the ACLs and the update
// time, and leaves the immutable creator, originating system and parent untouched, exactly like
// the Spanner update mutation.
var updateXidSQL = safesql.New(`UPDATE Xids
SET Acls = $2, UpdateTime = $3
WHERE Xid = $1`)

// FetchXidEntry retrieves a single XidEntry from storage by its unique numerical ID without
// locking it, so callers that write the entry back must read it with TxRepository.FetchXidEntry
// instead.
func (r *Repository) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	if r.db == nil {
		return nil, errNilPool()
	}
	return fetchXidEntry(ctx, r.db, selectXidSQL, xid)
}

// FetchXidEntry retrieves a single XidEntry from storage within the active read-write transaction
// and locks its row until the transaction ends.
func (r *TxRepository) FetchXidEntry(ctx context.Context, xid int64) (*xidpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	if r.tx == nil {
		return nil, errNilTx()
	}
	return fetchXidEntry(ctx, r.tx, lockXidSQL, xid)
}

// UpdateXidEntry writes the ACLs and update time of entry to the Xids row of xid within the active
// read-write transaction. It fails with NotFound when the row does not exist, as committing the
// Spanner update mutation does.
func (r *TxRepository) UpdateXidEntry(ctx context.Context, xid int64, entry *xidpb.XidEntry) error {
	if r.tx == nil {
		return errNilTx()
	}
	if entry == nil {
		return pgdb.WrapStatusError("entry must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	acls, err := marshalProto(entry.GetAcls())
	if err != nil {
		return fmt.Errorf("failed to marshal Acls: %w", err)
	}
	tag, err := r.tx.Exec(ctx, updateXidSQL.String(), xid, acls, entry.GetUpdateTime().AsTime())
	if err != nil {
		logging.Error(logging.WithXIDName(ctx, validator.ConstructXIDName(xid)), "Failed to update Xid row", "error", err)
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound(xid, nil)
	}
	return nil
}

// fetchXidEntry runs query, selectXidSQL or lockXidSQL, for xid on db and decodes the row.
func fetchXidEntry(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid int64) (*xidpb.XidEntry, error) {
	logging.Info(ctx, "Fetch XID entry row")
	entry, err := scanXidEntry(db.QueryRow(ctx, query.String(), xid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound(xid, err)
	}
	if err != nil {
		logging.Error(ctx, "Failed to read Xid row", "error", err)
		return nil, pgdb.TranslateError(err, entityName)
	}
	logging.Info(ctx, "Fetched XID entry from storage", "xid_entry", entry)
	return entry, nil
}
