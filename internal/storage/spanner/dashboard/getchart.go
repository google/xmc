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
	"google.golang.org/protobuf/proto"

	fepb "api/filter/filter_expression_go_proto"
	aipfilter "api/filter/go/filter"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// fetchChartPlots retrieves and unmarshals all PlotSpec child records for a chart.
func (r *Repository) fetchChartPlots(ctx context.Context, txn *spanner.ReadOnlyTransaction, chartId string) ([]*dpb.PlotSpec, error) {
	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("chart_id = %q", chartId))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for chart plots: %w", err)
	}

	stmt, _, _, err := r.chartPlotsQueryGen.GenerateQuery(
		[]querytools.TableField{dashboardschema.PlotSpecTableField},
		nil, nil, filterExp, nil, 0, 0, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate chart plots query: %w", err)
	}

	iter := txn.Query(ctx, *stmt)
	defer iter.Stop()

	var plots []*dpb.PlotSpec
	for {
		plotRow, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var plotSpecBytes []byte
		if err := plotRow.Columns(&plotSpecBytes); err != nil {
			return nil, err
		}
		var plotSpec dpb.PlotSpec
		if err := proto.Unmarshal(plotSpecBytes, &plotSpec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal plot spec: %w", err)
		}
		plots = append(plots, &plotSpec)
	}
	return plots, nil
}

// GetChart retrieves a chart by its unique ID along with its plot specifications and plot layout.
func (r *Repository) GetChart(ctx context.Context, chartId string) (*dpb.Chart, error) {
	logging.Info(ctx, "Get chart from storage", "chartId", chartId)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, dashboardschema.ChartsTableName, spanner.Key{chartId}, dashboardschema.FetchedChartColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to read chart %s: %w", chartId, err)
	}

	plots, err := r.fetchChartPlots(ctx, txn, chartId)
	if err != nil {
		return nil, err
	}

	return data.ChartRowToProto(row, plots)
}
