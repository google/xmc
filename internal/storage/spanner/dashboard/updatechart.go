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
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/internal/validator"
)

// UpdateChart applies a masked update to an existing chart and returns the stored result.
//
// The read, the merge and the write all happen inside one ReadWriteTransaction, the way
// UpdateDashboard already does it. Merging against a snapshot taken outside the transaction loses
// concurrent updates: the write overwrites every mutable column and replaces the whole plot set, so
// two callers updating disjoint fields would each persist their own view of the untouched ones and
// the later commit would silently discard the earlier one.
func (r *Repository) UpdateChart(ctx context.Context, chart *dpb.Chart, updateMask *fieldmaskpb.FieldMask) (*dpb.Chart, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if chart.GetName() == "" {
		return nil, fmt.Errorf("chart name must not be empty: %w", grpcerr.ErrInvalidArgument)
	}

	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}

	logging.Info(ctx, "Update chart in storage", "name", chart.GetName())

	var updatedChart *dpb.Chart
	commitTime, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		merged, err := mergeChartInTransaction(ctx, txn, chartID, chart, updateMask)
		if err != nil {
			return err
		}

		mutations, err := data.ChartProtoToUpdateMutations(merged)
		if err != nil {
			return err
		}

		updatedChart = merged
		return txn.BufferWrite(mutations)
	})
	if err != nil {
		logging.Error(ctx, "Failed to update chart", "name", chart.GetName(), "error", err)
		return nil, err
	}

	updatedChart.UpdateTime = timestamppb.New(commitTime)
	return updatedChart, nil
}

// mergeChartInTransaction reads the stored chart and applies the requested update on top of it.
func mergeChartInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, chartID string, chart *dpb.Chart, updateMask *fieldmaskpb.FieldMask) (*dpb.Chart, error) {
	existing, err := fetchChartInTransaction(ctx, txn, chartID)
	if err != nil {
		return nil, err
	}
	return data.MergeCharts(existing, chart, updateMask)
}

// fetchChartInTransaction reads a chart and its plot specifications inside a ReadWriteTransaction.
func fetchChartInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, chartID string) (*dpb.Chart, error) {
	row, err := readRowInTransaction(ctx, txn, dashboardschema.ChartsTableName, "chart", chartID, dashboardschema.FetchedChartColumns)
	if err != nil {
		return nil, err
	}

	plots, err := fetchChartPlotsInTransaction(ctx, txn, chartID)
	if err != nil {
		return nil, err
	}

	return data.ChartRowToProto(row, plots)
}

// fetchChartPlotsInTransaction reads the plot specifications of a chart via a Spanner prefix read.
//
// The plots have to be read here rather than reused from a caller's snapshot: an update replaces the
// entire plot set, so a stale set would undo plots another writer added in the meantime.
func fetchChartPlotsInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, chartID string) ([]*dpb.PlotSpec, error) {
	iter := txn.Read(ctx, dashboardschema.ChartPlotsTableName, spanner.Key{chartID}.AsPrefix(), []string{"PlotSpec"})
	defer iter.Stop()

	var plots []*dpb.PlotSpec
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read plots of chart %s: %w", chartID, err)
		}

		var plotSpecBytes []byte
		if err := row.Columns(&plotSpecBytes); err != nil {
			return nil, fmt.Errorf("failed to decode plot row of chart %s: %w", chartID, err)
		}
		plot, err := data.UnmarshalPlotSpec(chartID, plotSpecBytes)
		if err != nil {
			return nil, err
		}
		plots = append(plots, plot)
	}

	return plots, nil
}
