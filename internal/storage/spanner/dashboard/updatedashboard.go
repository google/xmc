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

	"task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
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
		existing, err := fetchDashboardInTransaction(ctx, txn, dashboard.GetName())
		if err != nil {
			return err
		}

		merged, err := data.MergeDashboards(existing, dashboard, updateMask)
		if err != nil {
			return err
		}
		// Sort charts canonically by Name to ensure consistent ordering with Spanner storage reads and accurate proto.Equal no-op detection.
		merged.Charts = data.SortCharts(merged.GetCharts())

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
func fetchDashboardInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, dashboardId string) (*dpb.Dashboard, error) {
	dashID := data.ExtractDashboardID(dashboardId)
	row, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashID}, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		if err == spanner.ErrRowNotFound || status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("dashboard %s not found: %w", dashboardId, status.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
	}

	charts, err := fetchDashboardChartsInTransaction(ctx, txn, dashID)
	if err != nil {
		return nil, err
	}

	return data.DashboardRowToProto(row, charts)
}

// fetchDashboardChartsInTransaction reads child chart IDs for a given dashboard via Spanner prefix read.
func fetchDashboardChartsInTransaction(ctx context.Context, txn *spanner.ReadWriteTransaction, dashboardId string) ([]*dpb.Chart, error) {
	dashID := data.ExtractDashboardID(dashboardId)
	iter := txn.Read(ctx, dashboardschema.DashboardChartsTableName, spanner.Key{dashID}.AsPrefix(), []string{"ChartId"})
	defer iter.Stop()

	var charts []*dpb.Chart
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
		charts = append(charts, &dpb.Chart{Name: chartId})
	}

	return charts, nil
}

// chartsEqual checks if two slices of charts contain identical chart names regardless of order using an O(N) frequency map.
func chartsEqual(a, b []*dpb.Chart) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	nilA, nilB := 0, 0
	for _, c := range a {
		if c == nil {
			nilA++
			continue
		}
		counts[c.GetName()]++
	}
	for _, c := range b {
		if c == nil {
			nilB++
			continue
		}
		counts[c.GetName()]--
		if counts[c.GetName()] < 0 {
			return false
		}
	}
	if nilA != nilB {
		return false
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
