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

// UpdateChart applies a masked update to an existing chart and returns the stored result, stamped
// with the update time the database assigned.
//
// The read, the merge and the write all happen inside one transaction, and the read locks the
// Charts row with FOR NO KEY UPDATE. Merging against a snapshot another writer may have changed
// since loses that writer's update: the write overwrites every mutable column and replaces the
// whole plot set, so two callers updating disjoint fields would each persist their own view of the
// untouched ones and the later commit would silently discard the earlier one.
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
	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// The callback may run more than once; start every attempt from the stored state.
		updatedChart = nil

		existing, err := fetchChartForUpdate(ctx, tx, chartID)
		if err != nil {
			return err
		}
		merged, err := storagedata.MergeCharts(existing, chart, updateMask)
		if err != nil {
			return err
		}

		updatedAt, err := writeChartUpdate(ctx, tx, chartID, merged)
		if err != nil {
			return err
		}
		merged.UpdateTime = timestamppb.New(updatedAt)
		updatedChart = merged
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to update chart", "name", chart.GetName(), "error", err)
		return nil, err
	}
	return updatedChart, nil
}

// writeChartUpdate overwrites the mutable columns of the identified chart with merged and replaces
// its plot set. It returns the UpdatedAt the database assigned.
func writeChartUpdate(ctx context.Context, tx pgx.Tx, chartID string, merged *dpb.Chart) (time.Time, error) {
	layoutBytes, err := marshalLayout(merged.GetPlotLayout(), "plot")
	if err != nil {
		return time.Time{}, err
	}
	plots, err := newPlotInserts(merged.GetPlots())
	if err != nil {
		return time.Time{}, err
	}

	var updatedAt time.Time
	if err := tx.QueryRow(ctx, updateChartSQL.String(), chartID, merged.GetTitle(), merged.GetDescription(), merged.GetWid(), layoutBytes).Scan(&updatedAt); err != nil {
		return time.Time{}, translateRowError(err, chartEntity, chartID)
	}
	if _, err := tx.Exec(ctx, deleteChartPlotsSQL.String(), chartID); err != nil {
		return time.Time{}, pgdb.TranslateError(err, "chart plot")
	}
	if err := insertChartPlots(ctx, tx, chartID, plots); err != nil {
		return time.Time{}, err
	}
	return updatedAt, nil
}
