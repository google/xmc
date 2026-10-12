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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// GetDashboard retrieves a dashboard by its unique ID along with its layout and child chart IDs.
func (r *Repository) GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
	if dashboardId == "" {
		return nil, fmt.Errorf("dashboardId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	logging.Info(ctx, "Get dashboard from storage", "dashboardId", dashboardId)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashboardId}, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		if err == spanner.ErrRowNotFound || status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("dashboard %s not found: %w", dashboardId, grpcerr.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
	}

	iter := txn.Read(ctx, dashboardschema.DashboardChartsTableName, spanner.Key{dashboardId}.AsPrefix(), []string{"ChartId"})
	defer iter.Stop()

	var chartIDs []string
	for {
		dcRow, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read child chart IDs for dashboard %s: %w", dashboardId, err)
		}
		var chartID string
		if err := dcRow.ColumnByName("ChartId", &chartID); err != nil {
			return nil, fmt.Errorf("failed to decode ChartId: %w", err)
		}
		chartIDs = append(chartIDs, chartID)
	}

	return data.DashboardRowToProto(row, chartIDs)
}
