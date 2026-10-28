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
package experiment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/experimentschema"
	"xmanager_cloud/internal/validator"
)

const (
	entityName              = "experiment"
	selectExperimentColumns = "Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, CreateTime, UpdateTime, CompleteTime"
)

var (
	_ storage.ExperimentStore        = (*Repository)(nil)
	_ storage.ExperimentTxRepository = (*TxRepository)(nil)

	fetchExperimentSQL           = safesql.New("SELECT " + selectExperimentColumns + " FROM Experiments WHERE Xid = $1")
	fetchExperimentForUpdateSQL  = safesql.New("SELECT " + selectExperimentColumns + " FROM Experiments WHERE Xid = $1 FOR NO KEY UPDATE")
	fetchExperimentStatusSQL     = safesql.New("SELECT Status FROM Experiments WHERE Xid = $1")
	insertExperimentSQL          = safesql.New("INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime, CompleteTime) VALUES (@xid, @title, @creator, @priority, @urls, @tags, @notes, @resources, @status, @activestate, @launchstate, @createtime, @updatetime, @completetime)")
	updateExperimentSQL          = safesql.New("UPDATE Experiments SET Title = @title, Priority = @priority, Urls = @urls, Tags = @tags, Notes = @notes, UpdateTime = @updatetime WHERE Xid = @xid")
	updateExperimentStatusSQL    = safesql.New("UPDATE Experiments SET Status = @status, ActiveState = @activestate, LaunchState = @launchstate, UpdateTime = @updatetime, CompleteTime = @completetime WHERE Xid = @xid")
	updateExperimentResourcesSQL = safesql.New("UPDATE Experiments SET Resources = @resources, UpdateTime = @updatetime WHERE Xid = @xid")
	fetchDriftedComponentsSQL    = safesql.New(
		"SELECT c.Name, s.LockedGeneration, c.ObservedGeneration, c.State, s.ComponentId " +
			"FROM ExperimentConsistencyInfo v " +
			"JOIN ExperimentComponentSnapshots s ON s.Xid = v.Xid " +
			"LEFT JOIN Components c ON s.ComponentId = c.ComponentId " +
			"WHERE v.Xid = $1 " +
			"AND NOT v.IsExperimentConsistent " +
			"AND (c.ComponentId IS NULL OR c.State = $2 OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration)) " +
			"ORDER BY c.Name ASC NULLS FIRST, s.ComponentId ASC " +
			"LIMIT (CASE WHEN $3 > 0 THEN $3 END)",
	)
)

// Repository manages persistence for experiment entities in PostgreSQL.
type Repository struct {
	pool                *pgxpool.Pool
	db                  pgdb.DBTX
	experimentsQueryGen querytools.QueryGenerator
}

// TxRepository manages transactional persistence for experiment entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Experiment repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	queryGen, err := querytools.NewQueryGenerator(
		experimentschema.ExperimentTables, nil, nil, nil,
		querytools.DeduplicateSelects(true),
	)
	if err != nil {
		logging.Error(context.Background(), "Failed to initialize experiment QueryGenerator", "error", err)
	}
	return &Repository{pool: pool, db: pgdb.FromPool(pool), experimentsQueryGen: queryGen}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return NewTxRepository(tx)
}

// NewTxRepository creates a transaction-scoped PostgreSQL experiment repository.
func NewTxRepository(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// FetchExperiment retrieves an experiment by its unique ID.
func (r *Repository) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	return fetchExperiment(ctx, r.db, fetchExperimentSQL, xid)
}

// FetchExperimentStatus retrieves only the status of an experiment by its unique ID.
func (r *Repository) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if xid == 0 {
		return nil, fmt.Errorf("experiment ID cannot be 0: %w", grpcerr.ErrInvalidArgument)
	}
	row, err := pgdb.QueryRow[postgresdata.ExperimentRow](ctx, r.db, fetchExperimentStatusSQL.String(), entityName, xid)
	if err != nil {
		return nil, err
	}
	statusProto, err := postgresdata.UnmarshalProto[essmsgpb.ExperimentStatus](row.Status)
	if err != nil {
		return nil, pgdb.WrapStatusError("failed to unmarshal experiment status", grpcerr.ErrInternal, err)
	}
	return statusProto, nil
}

// ListExperiments retrieves a list of experiments matching the given filter.
func (r *Repository) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, _ time.Duration) ([]*essmsgpb.Experiment, string, error) {
	if pageSize <= 0 {
		return nil, "", fmt.Errorf("page_size must be positive: %w", grpcerr.ErrInvalidArgument)
	}
	return pgdb.List(ctx, r.db, entityName, pageToken, liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                filter,
		OrderBy:               orderBy,
		QueryGen:              r.experimentsQueryGen,
		DefaultSelectFields:   experimentschema.ExperimentsSelectFields,
		DefaultFullTextFields: experimentschema.ExperimentsFullTextFields,
		ValidOrderByFields:    experimentschema.ExperimentsOrderByFields,
	}, postgresdata.ExperimentRowToProto, ExtractExperimentCursor)
}

// ExtractExperimentCursor extracts keyset pagination cursor values from an Experiment proto.
func ExtractExperimentCursor(exp *essmsgpb.Experiment) (map[string]any, error) {
	if exp == nil {
		return nil, fmt.Errorf("experiment cannot be nil")
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to parse experiment name %q: %w", exp.GetName(), err)
	}
	cursorValues := map[string]any{
		"xid":       xid,
		"created":   nil,
		"updated":   nil,
		"completed": nil,
	}
	if exp.GetCreateTime() != nil {
		cursorValues["created"] = exp.GetCreateTime().AsTime()
	}
	if exp.GetUpdateTime() != nil {
		cursorValues["updated"] = exp.GetUpdateTime().AsTime()
	}
	if exp.GetCompleteTime() != nil {
		cursorValues["completed"] = exp.GetCompleteTime().AsTime()
	}
	return cursorValues, nil
}

// UpdateExperiment updates an experiment with the given update mask.
func (r *Repository) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (storage.UpdatedExperiments, error) {
	if err := validateExperimentUpdate(update); err != nil {
		return storage.UpdatedExperiments{}, err
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(update.XID))
	var res storage.UpdatedExperiments
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		res = storage.UpdatedExperiments{}
		originalExp, err := NewTxRepository(tx).FetchExperiment(ctx, update.XID)
		if err != nil {
			return err
		}
		updatedExp, err := data.MergeExperiments(originalExp, update.Experiment, update.UpdateMask)
		if err != nil {
			return err
		}
		if proto.Equal(originalExp, updatedExp) {
			res = storage.UpdatedExperiments{Original: originalExp, Updated: updatedExp}
			return nil
		}
		updatedExp.UpdateTime = timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
		row, err := toExperimentRow(updatedExp)
		if err != nil {
			return err
		}
		if err := execExperimentStatement(ctx, tx, updateExperimentSQL, row.GetUpdateArgs(), true); err != nil {
			return err
		}
		res = storage.UpdatedExperiments{Original: originalExp, Updated: updatedExp}
		return nil
	})
	if err != nil {
		return storage.UpdatedExperiments{}, err
	}
	if !proto.Equal(res.Original, res.Updated) {
		protoDiff := diff.Diff(prototext.Format(res.Original), prototext.Format(res.Updated))
		logging.Info(ctx, fmt.Sprintf("Updated experiment: %s", protoDiff), "experiment", res.Updated)
	}
	return res, nil
}

type driftedComponentRow struct {
	Name               pgtype.Text `db:"name"`
	LockedGeneration   int64       `db:"lockedgeneration"`
	ObservedGeneration pgtype.Int8 `db:"observedgeneration"`
	State              pgtype.Int8 `db:"state"`
	ComponentID        int64       `db:"componentid"`
}

// FetchDriftedComponents retrieves components whose live catalog entry diverges from the experiment's snapshot.
func (r *Repository) FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if xid <= 0 {
		return nil, fmt.Errorf("experiment ID must be positive: %w", grpcerr.ErrInvalidArgument)
	}
	rows, err := pgdb.QueryRows[driftedComponentRow](ctx, r.db, fetchDriftedComponentsSQL.String(), entityName, xid, int64(storage.ComponentStateDeleted), limit)
	if err != nil {
		return nil, err
	}
	diffs := make([]storage.ComponentDiff, len(rows))
	for i, row := range rows {
		diffs[i] = toComponentDiff(row)
	}
	return diffs, nil
}

// FetchExperiment retrieves and locks an experiment by its unique ID within the transaction.
func (r *TxRepository) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	return fetchExperiment(ctx, r.tx, fetchExperimentForUpdateSQL, xid)
}

// InsertExperiment inserts a new experiment within the transaction.
func (r *TxRepository) InsertExperiment(ctx context.Context, exp *essmsgpb.Experiment) error {
	row, err := toExperimentRow(exp)
	if err != nil {
		return err
	}
	return execExperimentStatement(ctx, r.tx, insertExperimentSQL, row.GetInsertArgs(), false)
}

// UpdateExperimentStatus updates the experiment status within the transaction.
func (r *TxRepository) UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error {
	row, err := toExperimentRow(exp)
	if err != nil {
		return err
	}
	if st := exp.GetStatus().GetUpdateTime(); st != nil && (exp.GetUpdateTime() == nil || st.AsTime().After(row.UpdateTime)) {
		row.UpdateTime = st.AsTime()
	}
	return execExperimentStatement(ctx, r.tx, updateExperimentStatusSQL, row.GetStatusUpdateArgs(), true)
}

// UpdateExperimentResources updates the experiment resources within the transaction.
func (r *TxRepository) UpdateExperimentResources(ctx context.Context, exp *essmsgpb.Experiment) error {
	row, err := toExperimentRow(exp)
	if err != nil {
		return err
	}
	return execExperimentStatement(ctx, r.tx, updateExperimentResourcesSQL, row.GetResourcesUpdateArgs(), true)
}

func fetchExperiment(ctx context.Context, db pgdb.DBTX, sql safesql.TrustedSQLString, xid int64) (*essmsgpb.Experiment, error) {
	if xid == 0 {
		return nil, fmt.Errorf("experiment ID cannot be 0: %w", grpcerr.ErrInvalidArgument)
	}
	row, err := pgdb.QueryRow[postgresdata.ExperimentRow](ctx, db, sql.String(), entityName, xid)
	if err != nil {
		return nil, err
	}
	exp, err := postgresdata.ExperimentRowToProto(row)
	if err != nil {
		return nil, pgdb.WrapStatusError("failed to convert experiment row to proto", grpcerr.ErrInternal, err)
	}
	return exp, nil
}

func toExperimentRow(exp *essmsgpb.Experiment) (postgresdata.ExperimentRow, error) {
	if exp == nil {
		return postgresdata.ExperimentRow{}, pgdb.WrapStatusError("experiment must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	row, err := postgresdata.ExperimentProtoToRow(exp)
	if err != nil {
		return postgresdata.ExperimentRow{}, pgdb.WrapStatusError("failed to convert experiment to row", grpcerr.ErrInvalidArgument, err)
	}
	return row, nil
}

func execExperimentStatement(ctx context.Context, db pgdb.DBTX, sql safesql.TrustedSQLString, args pgx.StrictNamedArgs, requireRow bool) error {
	if db == nil {
		return pgdb.WrapStatusError("postgres database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	tag, err := db.Exec(ctx, sql.String(), args)
	if err != nil {
		return pgdb.TranslateError(err, entityName)
	}
	if requireRow && tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, entityName)
	}
	return nil
}

func validateExperimentUpdate(update *storage.ExperimentUpdate) error {
	if update == nil || update.Experiment == nil || update.UpdateMask == nil {
		return fmt.Errorf("update, experiment, and update mask must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if len(update.UpdateMask.GetPaths()) == 0 {
		return fmt.Errorf("update mask must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if update.XID == 0 {
		return fmt.Errorf("experiment ID must not be 0: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

func toComponentDiff(r driftedComponentRow) storage.ComponentDiff {
	d := storage.ComponentDiff{
		ComponentID:      r.ComponentID,
		LockedGeneration: r.LockedGeneration,
	}
	if r.Name.Valid {
		name := r.Name.String
		d.Name = &name
	}
	if r.ObservedGeneration.Valid {
		gen := r.ObservedGeneration.Int64
		d.ObservedGeneration = &gen
	}
	if r.State.Valid {
		state := r.State.Int64
		d.State = &state
	}
	return d
}
