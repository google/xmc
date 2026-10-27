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

// Package operation contains the concrete PostgreSQL repository implementation for operations.
package operation

import (
	"context"
	"fmt"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	anypb "google.golang.org/protobuf/types/known/anypb"
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

const entityName = "operation"

var (
	insertOperationSQL = safesql.New(`INSERT INTO Operations (Xid, WorkUnitId, OperationId, Done, Status, Response, OperationMetadata, CreateTime, UpdateTime)
VALUES (@xid, @workunitid, @operationid, @done, @status, @response, @operationmetadata, @createtime, @updatetime)`)

	selectOperationSQL = safesql.New(`SELECT Xid, WorkUnitId, OperationId, Done, Status, Response, OperationMetadata FROM Operations WHERE Xid = $1 AND WorkUnitId = $2 AND OperationId = $3`)

	// lockOperationSQL locks the row with FOR NO KEY UPDATE so foreign-key FOR KEY SHARE checks do not conflict.
	lockOperationSQL = safesql.Concat(selectOperationSQL, safesql.New(" FOR NO KEY UPDATE"))

	updateOperationSQL = safesql.New(`UPDATE Operations SET Done = @done, Status = @status, Response = @response, OperationMetadata = @operationmetadata, UpdateTime = @updatetime WHERE Xid = @xid AND WorkUnitId = @workunitid AND OperationId = @operationid`)
)

var _ storage.OperationStore = (*Repository)(nil)

// Repository manages persistence for operation entities in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL Operation repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// AddWorkUnitOperation adds an operation to a work unit.
func (r *Repository) AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error) {
	if operationMetadata == nil {
		return nil, pgdb.WrapStatusError("operationMetadata must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	ctx = logging.WithWorkUnitName(ctx, validator.ConstructWorkUnitName(xid, wid))
	logging.Info(ctx, "Add work unit operation to storage", "operationMetadata", operationMetadata)
	operation, row, err := buildNewOperationRow(xid, wid, uuid.NewString(), operationMetadata, time.Now())
	if err != nil {
		return nil, err
	}
	if _, err := r.pool.Exec(ctx, insertOperationSQL.String(), row.GetInsertArgs()); err != nil {
		logging.Error(ctx, "Failed to add work unit operation", "error", err)
		return nil, pgdb.TranslateError(err, entityName)
	}
	logging.Info(ctx, "Added work unit operation to storage", "operation", operation)
	return operation, nil
}

// FetchWorkUnitOperation retrieves an operation by its unique ID.
func (r *Repository) FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error) {
	ctx = logging.WithOperationName(ctx, validator.ConstructOperationName(xid, wid, operationID))
	logging.Info(ctx, "Fetch work unit operation")
	operation, err := queryOperation(ctx, r.pool, selectOperationSQL, xid, wid, operationID)
	if err != nil {
		return nil, err
	}
	logging.Info(ctx, "Fetched work unit operation", "operation", operation)
	return operation, nil
}

// UpdateWorkUnitOperation updates the status of an operation.
func (r *Repository) UpdateWorkUnitOperation(ctx context.Context, operationUpdate *storage.OperationUpdate) (*longrunningpb.Operation, error) {
	if err := validateUpdateRequest(operationUpdate); err != nil {
		return nil, err
	}
	ctx = logging.WithOperationName(ctx, validator.ConstructOperationName(operationUpdate.XID, operationUpdate.WID, operationUpdate.OID))
	logging.Info(ctx, "Update work unit operation", "update", operationUpdate)
	var originalOp, updatedOp *longrunningpb.Operation
	var mutated bool
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		mutated, originalOp, updatedOp = false, nil, nil
		var err error
		if originalOp, err = queryOperation(ctx, tx, lockOperationSQL, operationUpdate.XID, operationUpdate.WID, operationUpdate.OID); err != nil {
			return err
		}
		if updatedOp, err = data.MergeOperations(originalOp, operationUpdate.Operation, operationUpdate.UpdateMask); err != nil {
			return fmt.Errorf("failed to merge Operation: %w", err)
		}
		if proto.Equal(originalOp, updatedOp) {
			return nil
		}
		row, err := postgresdata.OperationProtoToRow(updatedOp, time.Now())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, updateOperationSQL.String(), row.GetUpdateArgs()); err != nil {
			return pgdb.TranslateError(err, entityName)
		}
		mutated = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	logOperationUpdate(ctx, mutated, originalOp, updatedOp)
	return updatedOp, nil
}

func validateUpdateRequest(u *storage.OperationUpdate) error {
	if u == nil || u.Operation == nil || len(u.UpdateMask.GetPaths()) == 0 {
		return pgdb.WrapStatusError("operationUpdate, operation, and updateMask must not be empty", grpcerr.ErrInvalidArgument, nil)
	}
	return nil
}

func buildNewOperationRow(xid, wid int64, oid string, metadata *essmsgpb.OperationMetadata, now time.Time) (*longrunningpb.Operation, postgresdata.OperationRow, error) {
	clonedMetadata := proto.Clone(metadata).(*essmsgpb.OperationMetadata)
	clonedMetadata.CreateTime = timestamppb.New(now)
	anyMetadata, err := anypb.New(clonedMetadata)
	if err != nil {
		return nil, postgresdata.OperationRow{}, fmt.Errorf("failed to marshal operation metadata to Any: %w", err)
	}
	op := &longrunningpb.Operation{Name: validator.ConstructOperationName(xid, wid, oid), Metadata: anyMetadata}
	row, err := postgresdata.OperationProtoToRow(op, now)
	return op, row, err
}

func queryOperation(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid, wid int64, oid string) (*longrunningpb.Operation, error) {
	row, err := pgdb.QueryRow[postgresdata.OperationRow](ctx, db, query.String(), entityName, xid, wid, oid)
	if err != nil {
		return nil, err
	}
	return postgresdata.OperationRowToProto(row)
}

func logOperationUpdate(ctx context.Context, mutated bool, original, updated *longrunningpb.Operation) {
	if !mutated {
		logging.Debug(ctx, "Work unit operation not updated; no changes detected", "updatedOperation", updated)
		return
	}
	logging.Info(ctx, fmt.Sprintf("Updated work unit operation: %s", diff.Diff(prototext.Format(original), prototext.Format(updated))), "updatedOperation", updated)
}
