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

// ListCharts returns a paginated list of Chart protos based on parent, filter, order, and cursor
// token. The only two parents served are "dashboards/{dashboard_id}", which lists the charts linked
// to the dashboard, and "experiments/{xid}/workUnits/{wid}"; an empty parent lists every chart.
// Each chart carries its plots, read in the same statement as the charts themselves. staleness is
// ignored: PostgreSQL reads are always strong.
func (r *Repository) ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
	logging.Info(ctx, "List charts", "parent", parent, "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	filter, err := storagedata.BuildListChartsFilter(parent, filter)
	if err != nil {
		return nil, "", err
	}

	queryGen, err := newChartsQueryGen()
	if err != nil {
		return nil, "", err
	}

	normalizedFilter := filter
	if filter != "" {
		normalizedFilter, err = normalizer.Normalize(filter, queryGen.Tables()[0])
		if err != nil {
			return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
		}
	}

	listParams := ls.ListParams{
		PageSize:            pageSize,
		Filter:              normalizedFilter,
		OrderBy:             orderBy,
		QueryGen:            queryGen,
		DefaultSelectFields: chartsSelectFields,
		ValidOrderByFields:  chartsOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt := postgresdialect.NewStatement("")
	if err := ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}
	logging.Debug(ctx, "Generated list statement", "sql", stmt.SQL, "args", stmt.Args)

	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		logging.Error(ctx, "Failed to query chart rows", "error", err)
		return nil, "", pgdb.TranslateError(err, chartEntity)
	}
	defer rows.Close()

	numColumns := len(rows.FieldDescriptions())
	var charts []*dpb.Chart
	for rows.Next() {
		row, err := scanChartRow(rows, numColumns)
		if err != nil {
			return nil, "", pgdb.TranslateError(fmt.Errorf("failed to read chart rows: %w", err), chartEntity)
		}
		chart, err := row.toProto()
		if err != nil {
			return nil, "", err
		}
		charts = append(charts, chart)
	}
	if err := rows.Err(); err != nil {
		logging.Error(ctx, "Failed to read chart rows", "error", err)
		return nil, "", pgdb.TranslateError(fmt.Errorf("failed to read chart rows: %w", err), chartEntity)
	}

	page, nextPageToken, err := ls.ApplyPagination(charts, pageSize, storagedata.ExtractChartCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed charts", "count", len(page), "nextPageToken", nextPageToken)
	return page, nextPageToken, nil
}
