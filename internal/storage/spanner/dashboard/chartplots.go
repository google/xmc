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

	fepb "xmanager_cloud/api/filter"
	aipfilter "xmanager_cloud/api/filter/go/filter"
	dpb "xmanager_cloud/api/protos/dashboard"
	spannerdialect "xmanager_cloud/internal/query_generator/dialect/spanner"
	"xmanager_cloud/internal/query_generator/querytools"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
)

// hydrateChartPlots populates the PlotSpec list of every chart in charts using a single batched
// query, so that list reads return the same fully populated protos as GetChart.
func (r *Repository) hydrateChartPlots(ctx context.Context, txn *spanner.ReadOnlyTransaction, charts []*dpb.Chart) error {
	chartIDs, err := storagedata.ExtractChartIDs(charts)
	if err != nil {
		return err
	}

	plotsByChartID, err := r.fetchPlotsByChartIDs(ctx, txn, chartIDs)
	if err != nil {
		return err
	}

	storagedata.AssignChartPlots(charts, chartIDs, plotsByChartID)
	return nil
}

// fetchPlotsByChartIDs retrieves the PlotSpec child records of every requested chart in one query,
// keyed by chart ID. Charts without plots are absent from the returned map.
func (r *Repository) fetchPlotsByChartIDs(ctx context.Context, txn *spanner.ReadOnlyTransaction, chartIDs []string) (map[string][]*dpb.PlotSpec, error) {
	plotsByChartID := map[string][]*dpb.PlotSpec{}
	if len(chartIDs) == 0 {
		return plotsByChartID, nil
	}

	stmt, err := r.buildChartPlotsStatement(chartIDs)
	if err != nil {
		return nil, err
	}

	iter := txn.Query(ctx, *stmt)
	defer iter.Stop()

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var chartID string
		var plotSpecBytes []byte
		if err := row.Columns(&chartID, &plotSpecBytes); err != nil {
			return nil, err
		}
		plotSpec, err := storagedata.UnmarshalPlotSpec(chartID, plotSpecBytes)
		if err != nil {
			return nil, err
		}
		plotsByChartID[chartID] = append(plotsByChartID[chartID], plotSpec)
	}
	return plotsByChartID, nil
}

// buildChartPlotsStatement generates the batched ChartPlots query for the given chart IDs.
func (r *Repository) buildChartPlotsStatement(chartIDs []string) (*spanner.Statement, error) {
	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), storagedata.BuildChartPlotsFilter(chartIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for chart plots: %w", err)
	}

	stmt := spannerdialect.NewStatement(nil)
	if _, _, err := r.chartPlotsQueryGen.GenerateQuery(
		stmt,
		[]querytools.TableField{dashboardschema.ChartIDTableField, dashboardschema.PlotSpecTableField},
		nil, nil, filterExp, nil, 0, 0, nil, nil,
	); err != nil {
		return nil, fmt.Errorf("failed to generate chart plots query: %w", err)
	}
	return stmt.Spanner(), nil
}
