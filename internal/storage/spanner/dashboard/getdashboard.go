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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// GetDashboard retrieves a dashboard by its unique ID along with its layout and child charts.
func (r *Repository) GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
	logging.Info(ctx, "Get dashboard from storage", "dashboardId", dashboardId)

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashboardId}, dashboardschema.FetchedDashboardColumns)
	if err != nil {
		return nil, fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
	}

	iter := txn.Query(ctx, spanner.Statement{
		SQL:    fmt.Sprintf("SELECT ChartId FROM %s WHERE DashboardId = @dashboardId", dashboardschema.DashboardChartsTableName),
		Params: map[string]interface{}{"dashboardId": dashboardId},
	})
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
