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
	"time"

	"github.com/jackc/pgx/v5"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// UpdateDashboard applies a masked update to an existing dashboard and returns the stored result.
//
// The read, the merge and the write all happen inside one transaction, and the read locks the
// Dashboards row with FOR NO KEY UPDATE: the write overwrites every mutable column, so merging
// against a snapshot another writer may have changed since would silently discard that writer's
// update. A dashboard that the merge leaves unchanged is not written at all. When the chart links
// change, they are replaced wholesale; a link to a chart that does not exist fails the foreign key
// check and surfaces as a FailedPrecondition error.
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
	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// The callback may run more than once; start every attempt from the stored state.
		updatedDashboard = nil

		existing, err := fetchDashboardForUpdate(ctx, tx, dashID)
		if err != nil {
			return err
		}
		plan, err := storagedata.PrepareDashboardUpdate(existing, dashboard, updateMask)
		if err != nil {
			return err
		}
		updatedDashboard = plan.Merged
		if !plan.Changed {
			return nil
		}

		updatedAt, err := writeDashboardUpdate(ctx, tx, dashID, plan)
		if err != nil {
			return err
		}
		updatedDashboard.UpdateTime = timestamppb.New(updatedAt)
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to update dashboard", "error", err)
		return nil, err
	}
	return updatedDashboard, nil
}

// writeDashboardUpdate writes the merged dashboard of plan and, when its chart links changed,
// replaces the DashboardCharts rows. It returns the UpdatedAt the database assigned.
func writeDashboardUpdate(ctx context.Context, tx pgx.Tx, dashID string, plan storagedata.DashboardUpdatePlan) (time.Time, error) {
	merged := plan.Merged
	layoutBytes, err := marshalLayout(merged.GetChartLayout(), "chart")
	if err != nil {
		return time.Time{}, err
	}
	var links []storagedata.DashboardChartLink
	if plan.UpdateChartIDs {
		links, err = storagedata.BuildDashboardChartLinks(merged)
		if err != nil {
			return time.Time{}, err
		}
	}

	var updatedAt time.Time
	if err := tx.QueryRow(ctx, updateDashboardSQL.String(), dashID, merged.GetTitle(), merged.GetDescription(), layoutBytes).Scan(&updatedAt); err != nil {
		return time.Time{}, translateRowError(err, dashboardEntity, dashID)
	}
	if !plan.UpdateChartIDs {
		return updatedAt, nil
	}
	if _, err := tx.Exec(ctx, deleteDashboardChartsSQL.String(), dashID); err != nil {
		return time.Time{}, pgdb.TranslateError(err, "dashboard chart link")
	}
	if err := insertDashboardChartLinks(ctx, tx, dashID, links); err != nil {
		return time.Time{}, err
	}
	return updatedAt, nil
}
