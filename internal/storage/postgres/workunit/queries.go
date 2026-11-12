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

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/offsetpagetoken"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/workunitschema"
	"xmanager_cloud/internal/validator"
)

// activeWorkUnitRow is a WorkUnits row joined with its parent experiment's creator and title.
type activeWorkUnitRow struct {
	postgresdata.WorkUnitRow
	Creator         string `db:"creator"`
	ExperimentTitle string `db:"experiment_title"`
}

// ListWorkUnits retrieves a list of work units matching the given filter.
func (r *Repository) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	if r == nil {
		return nil, "", pgdb.WrapStatusError("repository must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	ctx = logging.WithExperimentName(ctx, parent)
	params, err := r.listWorkUnitsParams(parent, pageSize, filter, orderBy)
	if err != nil {
		return nil, "", err
	}
	return pgdb.List(ctx, r.db, entityName, pageToken, params, postgresdata.WorkUnitRowToProto, ExtractWorkUnitCursor)
}

func (r *Repository) listWorkUnitsParams(parent string, pageSize int32, filter, orderBy string) (ls.ListParams, error) {
	if pageSize <= 0 {
		return ls.ListParams{}, pgdb.WrapStatusError("pageSize must be greater than zero", grpcerr.ErrInvalidArgument, nil)
	}
	xid, err := validator.ValidateExperimentName(parent)
	if err != nil {
		return ls.ListParams{}, fmt.Errorf("failed to validate experiment name: %w", err)
	}
	scopedFilter := fmt.Sprintf("xid = %d", xid)
	if filter != "" {
		scopedFilter = fmt.Sprintf("xid = %d AND (%s)", xid, filter)
	}
	return ls.ListParams{
		PageSize:              pageSize,
		Filter:                scopedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.workUnitsQueryGen,
		DefaultSelectFields:   workunitschema.WorkUnitSelectFields,
		DefaultFullTextFields: workunitschema.WorkUnitFullTextFields,
		ValidOrderByFields:    workunitschema.WorkUnitOrderByFields,
	}, nil
}

// ExtractWorkUnitCursor extracts pagination cursor values from a WorkUnit proto.
func ExtractWorkUnitCursor(wu *essmsgpb.WorkUnit) (map[string]any, error) {
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit name %q: %w", wu.GetName(), err)
	}
	execution := wu.GetCurrentWorkUnitExecution()
	cursor := map[string]any{
		"xid":                 xid,
		"work_unit_id":        wid,
		"state":               int64(execution.GetStatus().GetState()),
		"execution_completed": nil,
	}
	if wu.CreateTime != nil {
		cursor["created"] = wu.CreateTime.AsTime()
	}
	if wu.UpdateTime != nil {
		cursor["updated"] = wu.UpdateTime.AsTime()
	}
	if completeTime := execution.GetCompleteTime(); completeTime != nil {
		cursor["execution_completed"] = completeTime.AsTime()
	}
	return cursor, nil
}

// FetchActiveWorkUnitsByQueues retrieves active work units associated with the requested queue names.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string, query storage.ActiveWorkUnitQuery) (*storage.ActiveWorkUnitsResult, error) {
	var res *storage.ActiveWorkUnitsResult
	err := r.WithReadOnlyWorkUnitSnapshot(ctx, func(txCtx context.Context, repo storage.WorkUnitSnapshotRepository) error {
		var innerErr error
		res, innerErr = repo.FetchActiveWorkUnitsByQueues(txCtx, queueNames, pageSize, pageToken, query)
		return innerErr
	})
	return res, err
}

func fetchActiveWorkUnitsFromDB(ctx context.Context, db pgdb.DBTX, queueNames []string, pageSize int32, pageToken string, query storage.ActiveWorkUnitQuery) (*storage.ActiveWorkUnitsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(queueNames) == 0 {
		return &storage.ActiveWorkUnitsResult{WorkUnits: []storage.ActiveWorkUnit{}}, nil
	}
	pageSizeVal := offsetpagetoken.ClampPageSize(pageSize)
	offset, err := offsetpagetoken.Decode(pageToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w", err)
	}
	return fetchActiveWorkUnitsPageFromDB(ctx, db, queueNames, pageSizeVal, offset, query)
}

func fetchActiveWorkUnitsPageFromDB(ctx context.Context, db pgdb.DBTX, queueNames []string, pageSize int32, offset int64, query storage.ActiveWorkUnitQuery) (*storage.ActiveWorkUnitsResult, error) {
	count, fetch := activeWorkUnitStatements(queueNames, int64(pageSize)+1, offset, query)
	var totalQueueSize int64
	if err := db.QueryRow(ctx, count.sql.String(), count.args...).Scan(&totalQueueSize); err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	rawRows, err := pgdb.QueryRows[activeWorkUnitRow](ctx, db, fetch.sql.String(), entityName, fetch.args...)
	if err != nil {
		return nil, err
	}
	nextPageToken := ""
	if int64(len(rawRows)) > int64(pageSize) {
		rawRows = rawRows[:pageSize]
		nextPageToken = offsetpagetoken.Encode(offset + int64(pageSize))
	}
	workUnits, err := toActiveWorkUnits(rawRows)
	if err != nil {
		return nil, err
	}
	return &storage.ActiveWorkUnitsResult{WorkUnits: workUnits, NextPageToken: nextPageToken, TotalQueueSize: totalQueueSize}, nil
}

func toActiveWorkUnits(rows []activeWorkUnitRow) ([]storage.ActiveWorkUnit, error) {
	res := make([]storage.ActiveWorkUnit, 0, len(rows))
	for _, row := range rows {
		wu, err := postgresdata.WorkUnitRowToProto(row.WorkUnitRow)
		if err != nil {
			return nil, err
		}
		res = append(res, storage.ActiveWorkUnit{
			WorkUnit:        wu,
			Author:          row.Creator,
			ExperimentTitle: row.ExperimentTitle,
		})
	}
	return res, nil
}
