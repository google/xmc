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

	"cloud.google.com/go/spanner"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/grpcerr"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

// hydrateChartIDs fills in the chart IDs of the given dashboards from their DashboardCharts rows
// and returns them.
//
// The Dashboards row carries no chart references, so a listing that skips this step reports every
// dashboard as empty. The whole page is read in one pass over the child table rather than one read
// per dashboard, which keeps the cost of a listing independent of the page size.
func hydrateChartIDs(ctx context.Context, txn *spanner.ReadOnlyTransaction, dashboards []*dpb.Dashboard) ([]*dpb.Dashboard, error) {
	if len(dashboards) == 0 {
		return dashboards, nil
	}

	byID := make(map[string]*dpb.Dashboard, len(dashboards))
	keys := make([]spanner.KeySet, 0, len(dashboards))
	for _, dash := range dashboards {
		if dash == nil {
			continue
		}
		dashID, err := validator.ValidateDashboardName(dash.GetName())
		if err != nil {
			return nil, fmt.Errorf("invalid dashboard name %q: %w", dash.GetName(), err)
		}
		byID[dashID] = dash
		keys = append(keys, spanner.Key{dashID}.AsPrefix())
	}
	if len(keys) == 0 {
		return dashboards, nil
	}

	iter := txn.Read(ctx, dashboardschema.DashboardChartsTableName, spanner.KeySets(keys...), []string{"DashboardId", "ChartId"})
	defer iter.Stop()

	linked := make(map[string][]string, len(byID))
	if err := iter.Do(func(row *spanner.Row) error {
		var dashID, chartID string
		if err := row.Columns(&dashID, &chartID); err != nil {
			return fmt.Errorf("failed to decode dashboard chart link: %w", err)
		}
		linked[dashID] = append(linked[dashID], chartID)
		return nil
	}); err != nil {
		return nil, err
	}

	// Sort so that a listing serves chart IDs in the same canonical order GetDashboard does, rather
	// than in whatever order the child table read happened to return them.
	for dashID, chartIDs := range linked {
		if dash, ok := byID[dashID]; ok {
			dash.ChartIds = data.SortChartIDs(chartIDs)
		}
	}
	return dashboards, nil
}

// ListDashboards returns a paginated list of Dashboard protos matching the given filter and ordering.
func (r *Repository) ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
	logging.Info(ctx, "List dashboards", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	// 1. Normalize the filter expression.
	normalizedFilter, err := normalizer.Normalize(filter, r.dashboardsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2. Configure list statement parameters using query generator.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.dashboardsQueryGen,
		DefaultSelectFields:   dashboardschema.DashboardsSelectFields,
		DefaultFullTextFields: nil,
		ValidOrderByFields:    dashboardschema.DashboardsOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	// 3. Execute query on Spanner. A multi-use transaction is needed because the chart links live in
	// a second table and have to be read at the same snapshot as the dashboards themselves.
	txn := r.client.ReadOnlyTransaction()
	if staleness > 0 {
		txn = txn.WithTimestampBound(spanner.ExactStaleness(staleness))
	}
	defer txn.Close()

	iter := txn.Query(ctx, *stmt)
	defer iter.Stop()

	// 4. Convert rows into Dashboard protos.
	var dashboards []*dpb.Dashboard
	if err := iter.Do(func(row *spanner.Row) error {
		dash, err := data.DashboardRowToProto(row, nil)
		if err != nil {
			return err
		}
		dashboards = append(dashboards, dash)
		return nil
	}); err != nil {
		logging.Error(ctx, "Failed to read Dashboard rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Dashboard rows: %w", err)
	}

	// 5. Apply pagination cursor.
	dashboards, nextPageToken, err := ls.ApplyPagination(dashboards, pageSize, ExtractDashboardCursor)
	if err != nil {
		return nil, "", err
	}

	// 6. Resolve the chart links, after pagination so that the trimmed overflow row is not read.
	dashboards, err = hydrateChartIDs(ctx, txn, dashboards)
	if err != nil {
		logging.Error(ctx, "Failed to read dashboard chart links", "error", err)
		return nil, "", fmt.Errorf("failed to read dashboard chart links: %w", err)
	}

	logging.Info(ctx, "Listed dashboards", "count", len(dashboards), "nextPageToken", nextPageToken)
	return dashboards, nextPageToken, nil
}

// ExtractDashboardCursor extracts pagination cursor values from a Dashboard proto.
func ExtractDashboardCursor(dash *dpb.Dashboard) (map[string]any, error) {
	if dash == nil {
		return nil, fmt.Errorf("dashboard must not be nil: %w", grpcerr.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dash.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name in cursor: %w", err)
	}

	cursor := map[string]any{
		"dashboard_id": dashID,
	}

	if dash.GetCreateTime() != nil {
		cursor["created"] = dash.GetCreateTime().AsTime()
	} else {
		cursor["created"] = time.Time{}
	}
	if dash.GetUpdateTime() != nil {
		cursor["updated"] = dash.GetUpdateTime().AsTime()
	} else {
		cursor["updated"] = time.Time{}
	}

	return cursor, nil
}
