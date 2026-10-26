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

package dashboard

import (
	"context"
	"fmt"
	"time"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// ListDashboards returns a paginated list of Dashboard protos matching the given filter and
// ordering. Each dashboard carries its chart IDs, read in the same statement as the dashboards
// themselves. staleness is ignored: PostgreSQL reads are always strong.
func (r *Repository) ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
	logging.Info(ctx, "List dashboards", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	queryGen, err := newDashboardsQueryGen()
	if err != nil {
		return nil, "", err
	}

	// 1. Normalize the filter expression.
	normalizedFilter, err := normalizer.Normalize(filter, queryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2. Configure list statement parameters using query generator.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              queryGen,
		DefaultSelectFields:   dashboardsSelectFields,
		DefaultFullTextFields: nil,
		ValidOrderByFields:    dashboardsOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt := postgresdialect.NewStatement("")
	if err := ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}
	logging.Debug(ctx, "Generated list statement", "sql", stmt.SQL, "args", stmt.Args)

	// 3. Execute the query and convert the rows into Dashboard protos.
	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		logging.Error(ctx, "Failed to query Dashboard rows", "error", err)
		return nil, "", pgdb.TranslateError(err, dashboardEntity)
	}
	defer rows.Close()

	numColumns := len(rows.FieldDescriptions())
	var dashboards []*dpb.Dashboard
	for rows.Next() {
		row, err := scanDashboardRow(rows, numColumns)
		if err != nil {
			return nil, "", pgdb.TranslateError(fmt.Errorf("failed to read Dashboard rows: %w", err), dashboardEntity)
		}
		dash, err := row.toProto()
		if err != nil {
			return nil, "", err
		}
		dashboards = append(dashboards, dash)
	}
	if err := rows.Err(); err != nil {
		logging.Error(ctx, "Failed to read Dashboard rows", "error", err)
		return nil, "", pgdb.TranslateError(fmt.Errorf("failed to read Dashboard rows: %w", err), dashboardEntity)
	}

	// 4. Apply pagination cursor.
	dashboards, nextPageToken, err := ls.ApplyPagination(dashboards, pageSize, storagedata.ExtractDashboardCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed dashboards", "count", len(dashboards), "nextPageToken", nextPageToken)
	return dashboards, nextPageToken, nil
}
