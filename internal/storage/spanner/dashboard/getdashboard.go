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

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"

	fepb "api/filter/filter_expression_go_proto"
	aipfilter "api/filter/go/filter"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// GetDashboard retrieves a dashboard by its unique ID along with its layout and child charts.
func (r *Repository) GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
	dashID := data.ExtractDashboardID(dashboardId)
	logging.Info(ctx, "Get dashboard from storage", "dashboardId", dashID)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashID}, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
	}

	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("dashboard_id = %q", dashID))
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

	iter := txn.Query(ctx, *stmt)
	defer iter.Stop()

	var charts []*dpb.Chart
	for {
		dcRow, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var chartId string
		if err := dcRow.Columns(&chartId); err != nil {
			return nil, err
		}
		charts = append(charts, &dpb.Chart{Name: chartId})
	}

	return data.DashboardRowToProto(row, charts)
}
