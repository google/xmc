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

	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// DeleteDashboard deletes a dashboard and its child relationships from Spanner.
func (r *Repository) DeleteDashboard(ctx context.Context, dashboardId string) error {
	if dashboardId == "" {
		return fmt.Errorf("dashboardId must not be empty: %w", status.ErrInvalidArgument)
	}

	logging.Info(ctx, "Delete dashboard from storage", "dashboardId", dashboardId)

	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// 1. Verify that the dashboard exists.
		_, err := txn.ReadRow(ctx, dashboardschema.DashboardsTableName, spanner.Key{dashboardId}, []string{"DashboardId"})
		if err != nil {
			return fmt.Errorf("failed to read dashboard %s: %w", dashboardId, err)
		}

		// 2. Delete by Spanner mutation on primary key. Child DashboardCharts are deleted automatically via ON DELETE CASCADE.
		m := spanner.Delete(dashboardschema.DashboardsTableName, spanner.Key{dashboardId})
		return txn.BufferWrite([]*spanner.Mutation{m})
	})
	if err != nil {
		logging.Error(ctx, "Failed to delete dashboard", "dashboardId", dashboardId, "error", err)
		return err
	}

	logging.Info(ctx, "Deleted dashboard from storage", "dashboardId", dashboardId)
	return nil
}
