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
	"strings"
	"time"

	"cloud.google.com/go/spanner"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/validator/validator"
)

// ListCharts returns a paginated list of Chart protos based on parent, filter, order, and cursor token.
func (r *Repository) ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
	logging.Info(ctx, "List charts", "parent", parent, "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy)

	parentFilter, err := r.buildParentFilter(parent)
	if err != nil {
		return nil, "", err
	}

	filter = combineFilters(parentFilter, filter)
	normalizedFilter := filter
	if filter != "" {
		normalizedFilter, err = normalizer.Normalize(filter, r.chartsQueryGen.Tables()[0])
		if err != nil {
			return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
		}
	}

	listParams := ls.ListParams{
		PageSize:            pageSize,
		Filter:              normalizedFilter,
		OrderBy:             orderBy,
		QueryGen:            r.chartsQueryGen,
		DefaultSelectFields: dashboardschema.ChartsSelectFields,
		ValidOrderByFields:  dashboardschema.ChartsOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	txn := r.client.ReadOnlyTransaction().WithTimestampBound(spanner.ExactStaleness(staleness))
	defer txn.Close()

	iter := txn.Query(ctx, *stmt)
	defer iter.Stop()

	var charts []*dpb.Chart
	err = iter.Do(func(row *spanner.Row) error {
		chart, err := data.ChartRowToProto(row, nil)
		if err != nil {
			return err
		}
		charts = append(charts, chart)
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to read chart rows: %w", err)
	}

	// Paginate before hydrating so the over-fetched probe row never costs a plot lookup.
	page, nextPageToken, err := ls.ApplyPagination(charts, pageSize, ExtractChartCursor)
	if err != nil {
		return nil, "", err
	}
	if err := r.hydrateChartPlots(ctx, txn, page); err != nil {
		return nil, "", fmt.Errorf("failed to hydrate chart plots: %w", err)
	}
	return page, nextPageToken, nil
}

// ExtractChartCursor extracts pagination cursor values from a Chart proto.
func ExtractChartCursor(chart *dpb.Chart) (map[string]any, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart cannot be nil")
	}
	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to extract chart ID from %q: %w", chart.GetName(), err)
	}
	return map[string]any{
		"chart_id": chartID,
	}, nil
}

func combineFilters(parentFilter, filter string) string {
	if parentFilter == "" {
		return filter
	}
	if filter == "" {
		return parentFilter
	}
	return fmt.Sprintf("(%s) AND (%s)", parentFilter, filter)
}

func (r *Repository) buildParentFilter(parent string) (string, error) {
	if parent == "" {
		return "", nil
	}
	if strings.HasPrefix(parent, "dashboards/") || (!strings.HasPrefix(parent, "experiments/") && !strings.Contains(parent, "/")) {
		trimmed := strings.TrimPrefix(parent, "dashboards/")
		withPrefix := "dashboards/" + trimmed
		return fmt.Sprintf("dashboard_id = %q OR dashboard_id = %q", trimmed, withPrefix), nil
	}
	if strings.HasPrefix(parent, "experiments/") {
		if xid, wid, err := validator.ValidateWorkUnitName(parent); err == nil {
			return fmt.Sprintf("xid = %d AND wid = %d", xid, wid), nil
		}
		xid, err := validator.ValidateExperimentName(parent)
		if err != nil {
			return "", fmt.Errorf("failed to validate experiment parent name: %w", err)
		}
		return fmt.Sprintf("xid = %d", xid), nil
	}
	return "", fmt.Errorf("unsupported parent format: %q", parent)
}
