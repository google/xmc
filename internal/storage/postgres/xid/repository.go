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
//
// It is the PostgreSQL counterpart of internal/storage/spanner/xid and stores XidEntry protos in
// the Xids table of the XID database: Xid is a sequential IDENTITY column (Spanner uses a
// bit-reversed identity instead), the OriginatingSystem and Acls protos are serialized into BYTEA
// columns, and Endpoint is written from OriginatingSystem.endpoint on insert (Spanner derives it
// with a STORED generated column).
package xid

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/xidschema"
	"xmanager_cloud/internal/validator"
)

// entityName names XID entries in translated storage errors.
const entityName = "XID entry"

var (
	_ storage.XidEntryStore   = (*Repository)(nil)
	_ storage.XidTxRepository = (*TxRepository)(nil)
)

// xidsQueryGen builds the list query generator for the Xids table view on first use. The view is
// static, so one generator serves every Repository. Sort columns that are already selected are
// not selected a second time for the page token.
var xidsQueryGen = sync.OnceValues(func() (querytools.QueryGenerator, error) {
	return querytools.NewQueryGenerator(xidschema.XidTables, nil, nil, nil, querytools.DeduplicateSelects(true))
})

// Repository manages persistence for XID entry entities in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository manages transactional persistence for XID entry entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Xid repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// errNilPool reports a Repository that was created without a pool.
func errNilPool() error {
	return pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
}

// errNilTx reports a TxRepository that was bound to a nil transaction.
func errNilTx() error {
	return pgdb.WrapStatusError("postgres transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
}

// errNotFound reports that no Xids row has the given xid.
func errNotFound(xid int64, cause error) error {
	return pgdb.WrapStatusError(fmt.Sprintf("XID %d not found", xid), grpcerr.ErrNotFound, cause)
}

// xidRow holds the columns of one Xids row as scanned from PostgreSQL.
type xidRow struct {
	xid        int64
	parentXID  int64
	creator    string
	origin     []byte
	acls       []byte
	createTime time.Time
	updateTime time.Time
}

// scanTargets maps the Xids column names, lower-cased as PostgreSQL folds the schema's unquoted
// identifiers, to the fields of row that receive them.
func (row *xidRow) scanTargets() map[string]any {
	return map[string]any{
		"xid":               &row.xid,
		"parentxid":         &row.parentXID,
		"creator":           &row.creator,
		"originatingsystem": &row.origin,
		"acls":              &row.acls,
		"createtime":        &row.createTime,
		"updatetime":        &row.updateTime,
	}
}

// scanDestinations returns pgx.Rows.Scan destinations for the columns described by fields,
// matched by name so that the result does not depend on the column order of a generated query.
// Columns without a target, such as Endpoint or a column that is selected twice, are skipped with
// a nil destination. It fails if a column of the entry is missing.
func (row *xidRow) scanDestinations(fields []pgconn.FieldDescription) ([]any, error) {
	targets := row.scanTargets()
	dest := make([]any, len(fields))
	for i, field := range fields {
		name := strings.ToLower(field.Name)
		if target, ok := targets[name]; ok {
			dest[i] = target
			delete(targets, name)
		}
	}
	if len(targets) > 0 {
		return nil, fmt.Errorf("query result is missing Xids columns %v", slices.Sorted(maps.Keys(targets)))
	}
	return dest, nil
}

// toProto converts the row into an XidEntry the same way the Spanner repository converts its
// rows: a ParentXid of 0 means the entry has no parent, and the proto columns are unmarshaled into
// always-present messages.
func (row *xidRow) toProto() (*xidpb.XidEntry, error) {
	entry := &xidpb.XidEntry{
		Name:       proto.String(validator.ConstructXIDName(row.xid)),
		Creator:    proto.String(row.creator),
		Origin:     &xidpb.OriginatingSystem{},
		Acls:       &xidpb.XidAcls{},
		CreateTime: timestamppb.New(row.createTime),
		UpdateTime: timestamppb.New(row.updateTime),
	}
	if row.parentXID != 0 {
		entry.ParentXid = proto.String(validator.ConstructXIDName(row.parentXID))
	}
	if err := proto.Unmarshal(row.origin, entry.Origin); err != nil {
		return nil, fmt.Errorf("failed to read column OriginatingSystem: %w", err)
	}
	if err := proto.Unmarshal(row.acls, entry.Acls); err != nil {
		return nil, fmt.Errorf("failed to read column Acls: %w", err)
	}
	return entry, nil
}

// scanXidEntry decodes the single row of a query that selects xidschema.FetchedXIDColumns in that
// order. It returns pgx.ErrNoRows when the query matched nothing.
func scanXidEntry(row pgx.Row) (*xidpb.XidEntry, error) {
	var r xidRow
	if err := row.Scan(&r.xid, &r.parentXID, &r.creator, &r.origin, &r.acls, &r.createTime, &r.updateTime); err != nil {
		return nil, err
	}
	return r.toProto()
}

// scanXidEntries decodes every row of rows, which must include the columns of
// xidschema.FetchedXIDColumns in any order, and closes rows.
func scanXidEntries(rows pgx.Rows) ([]*xidpb.XidEntry, error) {
	defer rows.Close()
	var (
		row     xidRow
		dest    []any
		entries []*xidpb.XidEntry
	)
	for rows.Next() {
		if dest == nil {
			var err error
			if dest, err = row.scanDestinations(rows.FieldDescriptions()); err != nil {
				return nil, err
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		entry, err := row.toProto()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// marshalProto serializes m for a BYTEA NOT NULL column. It never returns a nil slice, which pgx
// would send as SQL NULL: a nil or empty message serializes to zero bytes.
func marshalProto(m proto.Message) ([]byte, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = []byte{}
	}
	return b, nil
}

// parentXID parses the parent XID name of entry into the ParentXid column value, 0 when the entry
// has no parent.
func parentXID(entry *xidpb.XidEntry) (int64, error) {
	if entry.GetParentXid() == "" {
		return 0, nil
	}
	parent, err := validator.ValidateXIDName(entry.GetParentXid())
	if err != nil {
		return 0, fmt.Errorf("failed to parse parent XID: %w", err)
	}
	return parent, nil
}
