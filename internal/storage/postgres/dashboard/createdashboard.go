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

	"github.com/jackc/pgx/v5"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// CreateDashboard writes a new dashboard, the charts created with it and their child
// dashboard-chart relationships to PostgreSQL in a single transaction.
//
// The charts share the dashboard's transaction so that they are committed atomically:
// DashboardCharts.ChartId is a foreign key onto Charts, so links to charts written by the same
// request are only ever valid when both land together. A dashboard or chart whose ID is already
// taken fails with an AlreadyExists error, and a link to a chart that does not exist with a
// FailedPrecondition error; neither leaves any of the rows behind.
func (r *Repository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
	logging.Info(ctx, "Create dashboard in storage", "name", dashboard.GetName(), "charts", len(charts))

	chartInserts := make([]*chartInsert, 0, len(charts))
	for _, chart := range charts {
		insert, err := newChartInsert(chart)
		if err != nil {
			logging.Error(ctx, "Failed to prepare inline chart for create dashboard", "error", err)
			return fmt.Errorf("invalid chart %q: %w", chart.GetName(), err)
		}
		chartInserts = append(chartInserts, insert)
	}
	insert, err := newDashboardInsert(dashboard)
	if err != nil {
		logging.Error(ctx, "Failed to prepare create dashboard", "error", err)
		return err
	}

	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// Charts first: the links inserted with the dashboard reference them.
		for _, chart := range chartInserts {
			if err := insertChart(ctx, tx, chart); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, insertDashboardSQL.String(), insert.ID, insert.Title, insert.Description, insert.Owner, insert.ChartLayout); err != nil {
			return pgdb.TranslateError(err, dashboardEntity)
		}
		return insertDashboardChartLinks(ctx, tx, insert.ID, insert.Links)
	})
	if err != nil {
		logging.Error(ctx, "Failed to create dashboard", "error", err)
		return err
	}
	return nil
}
