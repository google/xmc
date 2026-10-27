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

// Package xid contains the concrete PostgreSQL repository implementation for XID entries.
package xid

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/xidschema"
	"xmanager_cloud/internal/validator"
)

const entityName = "xid"

var (
	insertXidSQL = safesql.New(`INSERT INTO Xids (ParentXid, Creator, OriginatingSystem, Endpoint, Acls, CreateTime, UpdateTime)
VALUES (@parentxid, @creator, @originatingsystem, @endpoint, @acls, @createtime, @updatetime)
RETURNING Xid`)

	selectXidSQL = safesql.New(`SELECT Xid, ParentXid, Creator, OriginatingSystem, Endpoint, Acls, CreateTime, UpdateTime FROM Xids WHERE Xid = $1`)

	// lockXidSQL locks the row with FOR NO KEY UPDATE so foreign-key FOR KEY SHARE checks do not conflict.
	lockXidSQL = safesql.Concat(selectXidSQL, safesql.New(" FOR NO KEY UPDATE"))

	updateXidSQL = safesql.New(`UPDATE Xids SET Acls = @acls, UpdateTime = @updatetime WHERE Xid = @xid`)
)

var (
	_ storage.XidEntryStore   = (*Repository)(nil)
	_ storage.XidTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for XID entry entities in PostgreSQL.
type Repository struct {
	db           pgdb.DBTX
	xidsQueryGen querytools.QueryGenerator
}

// TxRepository manages transactional persistence for XID entry entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Xid repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	qgen, err := querytools.NewQueryGenerator(xidschema.XidTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		panic(fmt.Sprintf("failed to initialize xids query generator: %v", err))
	}
	return &Repository{db: pgdb.FromPool(pool), xidsQueryGen: qgen}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// CreateXidEntry creates a new XidEntry.
func (r *Repository) CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error) {
	if entry == nil {
		return nil, pgdb.WrapStatusError("entry must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	now := timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
	created := proto.Clone(entry).(*xidmsgpb.XidEntry)
	created.CreateTime = now
	created.UpdateTime = now
	row, err := postgresdata.XidProtoToRow(created)
	if err != nil {
		return nil, pgdb.WrapStatusError("invalid XidEntry", grpcerr.ErrInvalidArgument, err)
	}
	inserted, err := pgdb.QueryRow[postgresdata.XidRow](ctx, r.db, insertXidSQL.String(), entityName, row.GetInsertArgs())
	if err != nil {
		logging.Error(ctx, "Failed to create XID entry", "error", err)
		return nil, err
	}
	created.Name = proto.String(validator.ConstructXIDName(inserted.Xid))
	return created, nil
}

// FetchXidEntry retrieves an XidEntry by its unique ID.
func (r *Repository) FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error) {
	return queryXidEntry(ctx, r.db, selectXidSQL, xid)
}

// ListXidEntries retrieves a paginated and filtered list of XidEntries.
func (r *Repository) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, _ time.Duration) ([]*xidmsgpb.XidEntry, string, error) {
	return pgdb.List(ctx, r.db, entityName, pageToken, liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                filter,
		OrderBy:               orderBy,
		QueryGen:              r.xidsQueryGen,
		DefaultSelectFields:   xidschema.XidsSelectFields,
		DefaultFullTextFields: xidschema.XidsFullTextFields,
		ValidOrderByFields:    xidschema.XidsOrderByFields,
	}, postgresdata.XidRowToProto, ExtractXidEntryCursor)
}

// FetchXidEntry retrieves an XidEntry by its unique ID within the transaction and locks the row.
func (r *TxRepository) FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error) {
	if r == nil || r.tx == nil {
		return nil, pgdb.WrapStatusError("postgres transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return queryXidEntry(ctx, r.tx, lockXidSQL, xid)
}

// UpdateXidEntry persists an updated XidEntry within the transaction.
func (r *TxRepository) UpdateXidEntry(ctx context.Context, xid int64, entry *xidmsgpb.XidEntry) error {
	if r == nil || r.tx == nil {
		return pgdb.WrapStatusError("postgres transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	if entry == nil {
		return pgdb.WrapStatusError("entry must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	row, err := postgresdata.XidProtoToRow(entry)
	if err != nil {
		return pgdb.WrapStatusError("invalid XidEntry", grpcerr.ErrInvalidArgument, err)
	}
	row.Xid = xid
	tag, err := r.tx.Exec(ctx, updateXidSQL.String(), row.GetUpdateArgs())
	if err != nil {
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, entityName)
	}
	return nil
}

// ExtractXidEntryCursor extracts pagination cursor values from an XidEntry proto.
func ExtractXidEntryCursor(entry *xidmsgpb.XidEntry) (map[string]any, error) {
	xid, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate XID name %q: %w", entry.GetName(), err)
	}
	cursor := map[string]any{"xid": xid}
	if entry.GetCreateTime() != nil {
		cursor["created"] = entry.GetCreateTime().AsTime()
	}
	if entry.GetUpdateTime() != nil {
		cursor["updated"] = entry.GetUpdateTime().AsTime()
	}
	return cursor, nil
}

func queryXidEntry(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid int64) (*xidmsgpb.XidEntry, error) {
	ctx = logging.WithXIDName(ctx, validator.ConstructXIDName(xid))
	logging.Info(ctx, "Fetch XID entry row")
	row, err := pgdb.QueryRow[postgresdata.XidRow](ctx, db, query.String(), entityName, xid)
	if err != nil {
		return nil, err
	}
	entry, err := postgresdata.XidRowToProto(row)
	if err != nil {
		logging.Error(ctx, "Failed to extract XidEntry", "error", err)
		return nil, err
	}
	logging.Info(ctx, "Fetched XID entry from storage", "xid_entry", entry)
	return entry, nil
}
