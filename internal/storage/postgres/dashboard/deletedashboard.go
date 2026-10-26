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

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// DeleteDashboard deletes a dashboard and its child relationships from PostgreSQL. The charts the
// dashboard linked to are left in place. A dashboard that does not exist is reported as a NotFound
// error.
func (r *Repository) DeleteDashboard(ctx context.Context, dashboardID string) error {
	if dashboardID == "" {
		return fmt.Errorf("dashboardId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}

	logging.Info(ctx, "Delete dashboard from storage", "dashboardId", dashboardID)

	// A single statement is atomic: the DashboardCharts rows are deleted by ON DELETE CASCADE, and
	// the affected row count tells a missing dashboard apart from a deleted one.
	tag, err := r.db.Exec(ctx, deleteDashboardSQL.String(), dashboardID)
	if err != nil {
		logging.Error(ctx, "Failed to delete dashboard", "dashboardId", dashboardID, "error", err)
		return pgdb.TranslateError(err, dashboardEntity)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("dashboard %s not found: %w", dashboardID, grpcerr.ErrNotFound)
	}

	logging.Info(ctx, "Deleted dashboard from storage", "dashboardId", dashboardID)
	return nil
}
