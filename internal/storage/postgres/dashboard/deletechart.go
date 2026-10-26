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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// DeleteChart deletes a chart by its unique ID in a single transaction, together with the
// ArtifactCharts and DashboardCharts rows that reference it and, via ON DELETE CASCADE, its plots.
// A chart that does not exist is reported as a NotFound error.
func (r *Repository) DeleteChart(ctx context.Context, chartID string) error {
	if chartID == "" {
		return fmt.Errorf("chartId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}

	logging.Info(ctx, "Delete chart from storage", "chartId", chartID)

	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// 1. Verify that the chart exists, locking its row so that a concurrent UpdateChart either
		// finishes first or finds the chart gone.
		var one int
		if err := tx.QueryRow(ctx, lockChartKeySQL.String(), chartID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("chart %s not found: %w", chartID, grpcerr.ErrNotFound)
			}
			return pgdb.TranslateError(fmt.Errorf("failed to read chart %s: %w", chartID, err), chartEntity)
		}

		// 2. Remove the rows that reference the chart through non-cascading foreign keys.
		if _, err := tx.Exec(ctx, deleteArtifactChartReferencesSQL.String(), chartID); err != nil {
			return pgdb.TranslateError(err, "artifact chart")
		}
		if _, err := tx.Exec(ctx, deleteDashboardChartReferencesSQL.String(), chartID); err != nil {
			return pgdb.TranslateError(err, "dashboard chart link")
		}

		// 3. Delete the chart itself; ChartPlots rows are deleted by ON DELETE CASCADE.
		if _, err := tx.Exec(ctx, deleteChartSQL.String(), chartID); err != nil {
			return pgdb.TranslateError(err, chartEntity)
		}
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to delete chart", "chartId", chartID, "error", err)
		return err
	}

	logging.Info(ctx, "Deleted chart from storage", "chartId", chartID)
	return nil
}
