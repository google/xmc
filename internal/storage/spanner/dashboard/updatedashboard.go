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
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	fepb "api/filter/filter_expression_go_proto"
	aipfilter "api/filter/go/filter"
	"task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// UpdateDashboard updates an existing dashboard in Spanner and returns the updated proto.
func (r *Repository) UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", status.ErrInvalidArgument)
	}
	if dashboard.GetName() == "" {
		return nil, fmt.Errorf("dashboard name must not be empty: %w", status.ErrInvalidArgument)
	}

	logging.Info(ctx, "Update dashboard in storage", "name", dashboard.GetName())

	var updatedDashboard *dpb.Dashboard
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		existing, err := fetchDashboardInTransaction(ctx, txn, r.dashboardChartsQueryGen, dashboard.GetName())
		if err != nil {
			return err
		}

		merged, err := data.MergeDashboards(existing, dashboard, updateMask)
		if err != nil {
			return err
		}

		updatedDashboard = merged
		if proto.Equal(existing, merged) {
			return nil
		}

		updateCharts := !chartsEqual(existing.GetCharts(), merged.GetCharts())
		mutations, err := data.DashboardProtoToUpdateMutations(merged, updateCharts)
		if err != nil {
			return err
		}

		return txn.BufferWrite(mutations)
	})
	if err != nil {
		logging.Error(ctx, "Failed to update dashboard", "error", err)
		return nil, err
	}

	return updatedDashboard, nil
}

// fetchDashboardInTransaction reads an existing dashboard and its child charts inside a ReadWriteTransaction.
func fetchDashboardInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, chartsQueryGen querytools.QueryGenerator, dashboardId string) (*dpb.Dashboard, error) {
	row, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashboardId}, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
	}

	charts, err := fetchDashboardChartsInTransaction(ctx, txn, chartsQueryGen, dashboardId)
	if err != nil {
		return nil, err
	}

	return data.DashboardRowToProto(row, charts)
}

// fetchDashboardChartsInTransaction queries child chart IDs for a given dashboard.
func fetchDashboardChartsInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, chartsQueryGen querytools.QueryGenerator, dashboardId string) ([]*dpb.Chart, error) {
	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("dashboard_id = %q", dashboardId))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for dashboard charts: %w", err)
	}

	stmt, _, _, err := chartsQueryGen.GenerateQuery(
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

	return charts, nil
}

// chartsEqual checks if two slices of charts have identical names.
func chartsEqual(a, b []*dpb.Chart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].GetName() != b[i].GetName() {
			return false
		}
	}
	return true
}
