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
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/workunitschema"
	"xmanager_cloud/internal/validator"
)

// newWorkUnitsQueryGen constructs a fresh query generator over the PostgreSQL WorkUnits table view
// for one ListWorkUnits call, because querytools.QueryGenerator records LastFromTable without
// synchronization during GenerateQuery.
func newWorkUnitsQueryGen() (querytools.QueryGenerator, error) {
	return querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil)
}

// ListWorkUnits returns a page of the work units of the experiment named parent that match the
// AIP-160 filter, ordered by orderBy or, when orderBy is empty, by the view's default order of
// work unit ID ascending, and the token of the next page. Page tokens are opaque cursors built by
// ExtractWorkUnitCursor. PostgreSQL serves no stale reads, so staleness is ignored.
func (r *Repository) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	ctx = logging.WithExperimentName(ctx, parent)
	logging.Info(ctx, "Fetch work units", "parent", parent, "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)
	if err := r.requireDB(); err != nil {
		return nil, "", err
	}

	// 1. Ensure the results are filtered to a single XID.
	xid, err := validator.ValidateExperimentName(parent)
	if err != nil {
		return nil, "", fmt.Errorf("failed to validate experiment name: %w", err)
	}

	// 2. Generate the list statement.
	stmt, err := buildListStatement(ctx, xid, pageSize, pageToken, filter, orderBy)
	if err != nil {
		return nil, "", err
	}
	logging.Debug(ctx, "Generated list statement", "sql", stmt.SQL, "args", stmt.Args)

	// 3. Execute the query and convert the rows to WorkUnit protos.
	workUnits, err := queryWorkUnits(ctx, r.db, stmt)
	if err != nil {
		logging.Error(ctx, "failed to read WorkUnit rows", "error", err)
		return nil, "", fmt.Errorf("failed to read WorkUnit rows: %w", err)
	}

	// 4. If there are more results than the page size, return the next page token.
	workUnits, nextPageToken, err := ls.ApplyPagination(workUnits, pageSize, ExtractWorkUnitCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Fetched work units", "count", len(workUnits), "nextPageToken", nextPageToken)
	return workUnits, nextPageToken, nil
}

// buildListStatement generates the PostgreSQL list query for the work units of experiment xid.
func buildListStatement(ctx context.Context, xid int64, pageSize int32, pageToken, filter, orderBy string) (*postgresdialect.Statement, error) {
	queryGen, err := newWorkUnitsQueryGen()
	if err != nil {
		return nil, fmt.Errorf("failed to create WorkUnits query generator: %w", err)
	}

	scopedFilter := fmt.Sprintf("xid = %d", xid)
	if filter != "" {
		scopedFilter = fmt.Sprintf("xid = %d AND (%s)", xid, filter)
	}
	normalizedFilter, err := normalizer.Normalize(scopedFilter, queryGen.Tables()[0])
	if err != nil {
		return nil, fmt.Errorf("failed to normalize filter: %w", err)
	}

	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              queryGen,
		DefaultSelectFields:   workunitschema.WorkUnitSelectFields,
		DefaultFullTextFields: workunitschema.WorkUnitFullTextFields,
		ValidOrderByFields:    workunitschema.WorkUnitOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, err
	}

	stmt := postgresdialect.NewStatement("")
	if err := ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, fmt.Errorf("failed to generate list statement: %w", err)
	}
	return stmt, nil
}

// queryWorkUnits runs stmt, which selects workunitschema.WorkUnitSelectFields, on db.
func queryWorkUnits(ctx context.Context, db pgdb.DBTX, stmt *postgresdialect.Statement) ([]*essmsgpb.WorkUnit, error) {
	rows, err := db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, pgdb.TranslateError(err, workUnitEntity)
	}
	workUnitRows, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[workUnitRow])
	if err != nil {
		return nil, pgdb.TranslateError(err, workUnitEntity)
	}
	workUnits := make([]*essmsgpb.WorkUnit, 0, len(workUnitRows))
	for _, row := range workUnitRows {
		workUnit, err := row.toProto()
		if err != nil {
			return nil, fmt.Errorf("failed to convert WorkUnit row to proto: %w", err)
		}
		workUnits = append(workUnits, workUnit)
	}
	return workUnits, nil
}

// ExtractWorkUnitCursor extracts the pagination cursor values of the work unit view's orderable
// fields from a WorkUnit proto. Unlike the Spanner cursor it carries no bit_reversed_xid, which
// the PostgreSQL view does not have.
func ExtractWorkUnitCursor(wu *essmsgpb.WorkUnit) (map[string]any, error) {
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit name %q: %w", wu.GetName(), err)
	}

	execution := wu.GetCurrentWorkUnitExecution()
	cursor := map[string]any{
		"xid":          xid,
		"work_unit_id": wid,
		"state":        execution.GetStatus().GetState().String(),
		// Nil until the execution completes; matches the NULL column for cursor comparisons.
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
