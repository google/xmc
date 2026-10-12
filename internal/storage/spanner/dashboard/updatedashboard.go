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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/validator/validator"
)

// UpdateDashboard updates an existing dashboard in Spanner and returns the updated proto.
func (r *Repository) UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if dashboard.GetName() == "" {
		return nil, fmt.Errorf("dashboard name must not be empty: %w", grpcerr.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}

	logging.Info(ctx, "Update dashboard in storage", "name", dashboard.GetName())

	var updatedDashboard *dpb.Dashboard
	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		existing, err := fetchDashboardInTransaction(ctx, txn, dashID)
		if err != nil {
			return err
		}

		merged, err := data.MergeDashboards(existing, dashboard, updateMask)
		if err != nil {
			return err
		}
		// Sort chart IDs canonically to ensure consistent ordering with Spanner storage reads and accurate proto.Equal no-op detection.
		merged.ChartIds = data.SortChartIDs(merged.GetChartIds())

		updatedDashboard = merged
		if proto.Equal(existing, merged) {
			return nil
		}

		updateChartIDs := !data.ChartIDsEqual(existing.GetChartIds(), merged.GetChartIds())
		mutations, err := data.DashboardProtoToUpdateMutations(merged, updateChartIDs)
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

// fetchDashboardInTransaction reads an existing dashboard and its child chart IDs inside a ReadWriteTransaction.
func fetchDashboardInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, dashboardId string) (*dpb.Dashboard, error) {
	row, err := readRowInTransaction(ctx, txn, dashboardschema.DashboardsTableName, "dashboard", dashboardId, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		return nil, err
	}

	chartIDs, err := fetchDashboardChartsInTransaction(ctx, txn, dashboardId)
	if err != nil {
		return nil, err
	}

	return data.DashboardRowToProto(row, chartIDs)
}

// fetchDashboardChartsInTransaction reads child chart IDs for a given dashboard via Spanner prefix read.
func fetchDashboardChartsInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, dashboardId string) ([]string, error) {
	iter := txn.Read(ctx, dashboardschema.DashboardChartsTableName, spanner.Key{dashboardId}.AsPrefix(), []string{"ChartId"})
	defer iter.Stop()

	var chartIDs []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var chartId string
		if err := row.Columns(&chartId); err != nil {
			return nil, err
		}
		chartIDs = append(chartIDs, chartId)
	}

	return chartIDs, nil
}
