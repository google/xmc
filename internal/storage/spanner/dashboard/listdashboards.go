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

	"task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

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

	// 3. Execute query on Spanner.
	tx := r.client.Single()
	if staleness > 0 {
		tx = tx.WithTimestampBound(spanner.ExactStaleness(staleness))
	}
	iter := tx.Query(ctx, *stmt)
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

	logging.Info(ctx, "Listed dashboards", "count", len(dashboards), "nextPageToken", nextPageToken)
	return dashboards, nextPageToken, nil
}

// ExtractDashboardCursor extracts pagination cursor values from a Dashboard proto.
func ExtractDashboardCursor(dash *dpb.Dashboard) (map[string]any, error) {
	if dash == nil {
		return nil, fmt.Errorf("dashboard must not be nil: %w", status.ErrInvalidArgument)
	}

	cursor := map[string]any{
		"dashboard_id": dash.GetName(),
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
