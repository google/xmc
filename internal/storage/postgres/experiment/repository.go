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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
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
)

// Repository manages persistence for experiment entities in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
	db   pgdb.DBTX
}

// TxRepository manages transactional persistence for experiment entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL Experiment repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, db: pgdb.FromPool(pool)}
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
func (r *Repository) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres experiment store: ListExperiments is not implemented")
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

// FetchDriftedComponents retrieves components whose live catalog entry diverges from the experiment's snapshot.
func (r *Repository) FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error) {
	return nil, status.Error(codes.Unimplemented, "postgres experiment store: FetchDriftedComponents is not implemented")
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
