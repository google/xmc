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

// Package experiment contains the concrete PostgreSQL repository implementation for experiments.
//
// It mirrors internal/storage/spanner/experiment: the same interface, semantics, and gRPC error
// codes, with the Experiments table of internal/storage/postgres/schema/ess-schema.sql as the
// source of truth for column names and types. Protos are stored as BYTEA (arrays of protos as
// BYTEA[]), timestamps as TIMESTAMPTZ, and the ActiveState and LaunchState columns, which Spanner
// derives from Status with generated columns, are populated from Status on every write of Status.
package experiment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

var (
	_ storage.ExperimentStore        = (*Repository)(nil)
	_ storage.ExperimentTxRepository = (*TxRepository)(nil)
)

var (
	// errNilPool is returned by Repository methods when the repository was created without a pool.
	errNilPool = pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	// errNilTx is returned by TxRepository methods when the repository is not bound to a
	// transaction; its text matches the Spanner implementation.
	errNilTx = errors.New("transaction must not be nil")
	// errNilExperiment is returned when a caller passes a nil experiment; its text matches the
	// Spanner implementation.
	errNilExperiment = errors.New("experiment must not be nil")
)

var (
	// selectExperimentSQL reads one experiment without locking it. ActiveState and LaunchState
	// are not read back: they are derived from Status, which the proto already carries.
	selectExperimentSQL = safesql.New(`SELECT Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, CreateTime, UpdateTime, CompleteTime
FROM Experiments
WHERE Xid = $1`)

	// lockExperimentSQL reads one experiment and locks its row until the transaction ends, so
	// that concurrent read-modify-write transactions run one after another instead of
	// overwriting each other at READ COMMITTED. FOR NO KEY UPDATE, unlike FOR UPDATE, does not
	// conflict with the FOR KEY SHARE locks that foreign-key checks take on the experiment when
	// child rows (work units, component snapshots) are inserted.
	lockExperimentSQL = safesql.Concat(selectExperimentSQL, safesql.New(`
FOR NO KEY UPDATE`))

	// selectExperimentStatusSQL reads only the serialized status of one experiment.
	selectExperimentStatusSQL = safesql.New(`SELECT Status
FROM Experiments
WHERE Xid = $1`)

	// insertExperimentSQL inserts a complete experiment row.
	insertExperimentSQL = safesql.New(`INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime, CompleteTime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`)

	// updateExperimentSQL writes the user-mutable columns of experimentschema.ExperimentUpdateColumns.
	updateExperimentSQL = safesql.New(`UPDATE Experiments
SET Title = $2, Priority = $3, Urls = $4, Tags = $5, Notes = $6, UpdateTime = $7
WHERE Xid = $1`)

	// updateExperimentStatusSQL writes the columns of experimentschema.ExperimentStatusUpdateColumns.
	updateExperimentStatusSQL = safesql.New(`UPDATE Experiments
SET Status = $2, ActiveState = $3, LaunchState = $4, UpdateTime = $5, CompleteTime = $6
WHERE Xid = $1`)

	// updateExperimentResourcesSQL writes the columns of experimentschema.ExperimentResourcesUpdateColumns.
	updateExperimentResourcesSQL = safesql.New(`UPDATE Experiments
SET Resources = $2, UpdateTime = $3
WHERE Xid = $1`)
)

// Repository manages persistence for experiment entities in PostgreSQL.
type Repository struct {
	// db runs the non-transactional statements; NewRepository binds it to the pool.
	db pgdb.DBTX
	// pool starts the transactions of self-contained read-modify-write operations such as
	// UpdateExperiment and the read-only transactions of multi-statement reads.
	pool *pgxpool.Pool
}

// TxRepository manages transactional persistence for experiment entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Experiment repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool), pool: pool}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// experimentEntity names one experiment in translated storage errors, so that a missing row
// reads "experiment 123 not found" like the Spanner implementation.
func experimentEntity(xid int64) string {
	return fmt.Sprintf("experiment %d", xid)
}

// errExperimentNotFound reports that the experiment xid does not exist.
func errExperimentNotFound(xid int64) error {
	return pgdb.WrapStatusError(fmt.Sprintf("%s not found", experimentEntity(xid)), grpcerr.ErrNotFound, nil)
}

// experimentRow holds the columns of one Experiments row as scanned from PostgreSQL.
type experimentRow struct {
	xid          int64
	title        string
	creator      string
	priority     int64
	urls         [][]byte
	tags         []string
	notes        string
	resources    [][]byte
	status       []byte
	createTime   time.Time
	updateTime   time.Time
	completeTime pgtype.Timestamptz
}

// scanExperiment scans one row of a query over the Experiments columns and converts it to an
// Experiment proto. Columns are matched by name, so it serves both the fixed statements of this
// package and the generated list statements, whatever their column order. It is a pgx.RowToFunc
// for pgx.CollectRows and pgx.CollectExactlyOneRow.
func scanExperiment(row pgx.CollectableRow) (*pb.Experiment, error) {
	var (
		r experimentRow
		// List statements select the ActiveState and LaunchState columns too; the proto carries
		// both inside Status, so they are scanned and discarded.
		activeState, launchState int64
	)
	fields := row.FieldDescriptions()
	dests := make([]any, len(fields))
	for i, fd := range fields {
		switch strings.ToLower(fd.Name) {
		case "xid":
			dests[i] = &r.xid
		case "title":
			dests[i] = &r.title
		case "creator":
			dests[i] = &r.creator
		case "priority":
			dests[i] = &r.priority
		case "urls":
			dests[i] = &r.urls
		case "tags":
			dests[i] = &r.tags
		case "notes":
			dests[i] = &r.notes
		case "resources":
			dests[i] = &r.resources
		case "status":
			dests[i] = &r.status
		case "activestate":
			dests[i] = &activeState
		case "launchstate":
			dests[i] = &launchState
		case "createtime":
			dests[i] = &r.createTime
		case "updatetime":
			dests[i] = &r.updateTime
		case "completetime":
			dests[i] = &r.completeTime
		default:
			return nil, fmt.Errorf("unexpected column %q in Experiments row", fd.Name)
		}
	}
	if err := row.Scan(dests...); err != nil {
		return nil, err
	}
	return r.toProto()
}

// toProto converts the scanned row to an Experiment proto. Like the Spanner row conversion, it
// returns empty rather than nil repeated fields for empty arrays.
func (r *experimentRow) toProto() (*pb.Experiment, error) {
	urls, err := unmarshalMessages[pb.Url](r.urls)
	if err != nil {
		return nil, fmt.Errorf("failed to read column Urls: %w", err)
	}
	resources, err := unmarshalMessages[pb.ResourceSet](r.resources)
	if err != nil {
		return nil, fmt.Errorf("failed to read column Resources: %w", err)
	}
	status := &pb.ExperimentStatus{}
	if err = proto.Unmarshal(r.status, status); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}
	tags := r.tags
	if tags == nil {
		tags = []string{}
	}
	exp := &pb.Experiment{
		Name:       validator.ConstructExperimentName(r.xid),
		Title:      r.title,
		Creator:    r.creator,
		Priority:   pb.Priority(r.priority),
		Urls:       urls,
		Tags:       tags,
		Notes:      r.notes,
		Resources:  resources,
		Status:     status,
		CreateTime: timestamppb.New(r.createTime),
		UpdateTime: timestamppb.New(r.updateTime),
	}
	if r.completeTime.Valid {
		exp.CompleteTime = timestamppb.New(r.completeTime.Time)
	}
	return exp, nil
}

// fetchExperimentRow runs query, which must select the columns of selectExperimentSQL for the
// experiment xid, and converts the single row. It returns pgx.ErrNoRows when the experiment does
// not exist.
func fetchExperimentRow(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid int64) (*pb.Experiment, error) {
	rows, err := db.Query(ctx, query.String(), xid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectExactlyOneRow(rows, scanExperiment)
}

// execExperimentUpdate runs an UPDATE of the experiment xid and reports a NotFound error when no
// row was updated, which is how the Spanner implementation's Update mutations fail for a missing
// row.
func execExperimentUpdate(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid int64, args ...any) error {
	tag, err := db.Exec(ctx, query.String(), append([]any{xid}, args...)...)
	if err != nil {
		return pgdb.TranslateError(err, experimentEntity(xid))
	}
	if tag.RowsAffected() == 0 {
		return errExperimentNotFound(xid)
	}
	return nil
}

// unmarshalMessages decodes each serialized element of raws into a new message of type M. It
// returns an empty, non-nil slice for an empty array.
func unmarshalMessages[M any, PM interface {
	*M
	proto.Message
}](raws [][]byte) ([]PM, error) {
	msgs := make([]PM, 0, len(raws))
	for _, raw := range raws {
		msg := PM(new(M))
		if err := proto.Unmarshal(raw, msg); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// marshalMessages serializes msgs into the elements of a BYTEA[] column. A nil slice becomes SQL
// NULL, as it does in the Spanner implementation, which the NOT NULL columns reject; an empty
// slice becomes an empty array.
func marshalMessages[M proto.Message](msgs []M) ([][]byte, error) {
	if msgs == nil {
		return nil, nil
	}
	raws := make([][]byte, 0, len(msgs))
	for _, msg := range msgs {
		raw, err := proto.Marshal(msg)
		if err != nil {
			return nil, err
		}
		raws = append(raws, raw)
	}
	return raws, nil
}

// nullTimestamptz maps a nil timestamp to SQL NULL, matching the nullable CompleteTime column.
func nullTimestamptz(ts *timestamppb.Timestamp) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: ts.AsTime(), Valid: true}
}
