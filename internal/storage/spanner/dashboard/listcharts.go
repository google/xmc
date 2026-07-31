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
	"google.golang.org/api/iterator"

	fepb "api/filter/filter_expression_go_proto"
	aipfilter "api/filter/go/filter"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/validator/validator"
)

// ListCharts returns a paginated list of Chart protos based on parent, filter, order, and cursor token.
func (r *Repository) ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
	logging.Info(ctx, "List charts", "parent", parent, "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy)

	parentFilter, hasMatches, err := r.buildParentFilter(ctx, parent, staleness)
	if err != nil {
		return nil, "", err
	}
	if !hasMatches {
		return []*dpb.Chart{}, "", nil
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

	iter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
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

	return ls.ApplyPagination(charts, pageSize, ExtractChartCursor)
}

// ExtractChartCursor extracts pagination cursor values from a Chart proto.
func ExtractChartCursor(chart *dpb.Chart) (map[string]any, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart cannot be nil")
	}
	return map[string]any{
		"chart_id": chart.GetName(),
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

func (r *Repository) buildParentFilter(ctx context.Context, parent string, staleness time.Duration) (string, bool, error) {
	if parent == "" {
		return "", true, nil
	}
	if strings.HasPrefix(parent, "dashboards/") || (!strings.HasPrefix(parent, "experiments/") && !strings.Contains(parent, "/")) {
		chartIDs, err := r.fetchDashboardChartIDs(ctx, parent, staleness)
		if err != nil {
			return "", false, err
		}
		if len(chartIDs) == 0 {
			return "", false, nil
		}
		var clauses []string
		for _, id := range chartIDs {
			clauses = append(clauses, fmt.Sprintf("chart_id = %q", id))
		}
		return strings.Join(clauses, " OR "), true, nil
	}
	if strings.HasPrefix(parent, "experiments/") {
		xid, err := validator.ValidateExperimentName(parent)
		if err != nil {
			return "", false, fmt.Errorf("failed to validate experiment parent name: %w", err)
		}
		return fmt.Sprintf("xid = %d", xid), true, nil
	}
	return "", false, fmt.Errorf("unsupported parent format: %q", parent)
}

func (r *Repository) fetchDashboardChartIDs(ctx context.Context, parent string, staleness time.Duration) ([]string, error) {
	trimmed := strings.TrimPrefix(parent, "dashboards/")
	withPrefix := "dashboards/" + trimmed

	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("dashboard_id = %q OR dashboard_id = %q", parent, withPrefix))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for dashboard charts: %w", err)
	}

	stmt, _, _, err := r.dashboardChartsQueryGen.GenerateQuery(
		[]querytools.TableField{dashboardschema.ChartIDTableField},
		nil, nil, filterExp, nil, 0, 0, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate dashboard charts query: %w", err)
	}

	tx := r.client.Single()
	if staleness > 0 {
		tx = tx.WithTimestampBound(spanner.ExactStaleness(staleness))
	}
	iter := tx.Query(ctx, *stmt)
	defer iter.Stop()

	var chartIDs []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read dashboard chart row: %w", err)
		}
		var chartID string
		if err := row.Column(0, &chartID); err != nil {
			return nil, fmt.Errorf("failed to parse chart ID: %w", err)
		}
		chartIDs = append(chartIDs, chartID)
	}
	return chartIDs, nil
}
