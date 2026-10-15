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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
)

// fetchChartPlots retrieves and unmarshals all PlotSpec child records for a chart.
func (r *Repository) fetchChartPlots(ctx context.Context, txn *spanner.ReadOnlyTransaction, chartId string) ([]*dpb.PlotSpec, error) {
	plotsByChartID, err := r.fetchPlotsByChartIDs(ctx, txn, []string{chartId})
	if err != nil {
		return nil, err
	}
	return plotsByChartID[chartId], nil
}

// GetChart retrieves a chart by its unique ID along with its plot specifications and plot layout.
func (r *Repository) GetChart(ctx context.Context, chartId string) (*dpb.Chart, error) {
	logging.Info(ctx, "Get chart from storage", "chartId", chartId)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, dashboardschema.ChartsTableName, spanner.Key{chartId}, dashboardschema.FetchedChartColumns)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("chart %s not found: %w", chartId, grpcerr.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to read chart %s: %w", chartId, err)
	}

	plots, err := r.fetchChartPlots(ctx, txn, chartId)
	if err != nil {
		return nil, err
	}

	return data.ChartRowToProto(row, plots)
}
