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

// Package dashboard contains the concrete PostgreSQL repository implementation for dashboards and charts.
//
// It mirrors internal/storage/spanner/dashboard: the same validation, update-mask merging and
// resource naming through internal/storage/data and internal/validator, the same query generator
// views for ListDashboards and ListCharts, and the same gRPC error codes. Where Spanner reads a
// parent row and its interleaved children in two reads of one snapshot, this package selects the
// children as an array aggregate in the parent's statement, so every read is a single statement.
// Timestamps are set by the database to now(), the start time of the writing transaction, where
// Spanner uses the commit timestamp. The staleness argument of the List methods is accepted for
// interface compatibility and ignored: PostgreSQL reads are always strong.
package dashboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// Entity names used in translated storage errors.
const (
	dashboardEntity = "dashboard"
	chartEntity     = "chart"
)

// Dashboards statements.
var (
	insertDashboardSQL = safesql.New(`INSERT INTO Dashboards (DashboardId, Title, Description, Owner, ChartLayout, CreatedAt, UpdatedAt)
VALUES ($1, $2, $3, $4, $5, now(), now())`)

	// insertDashboardChartSQL links a chart to a dashboard. The link's own ID is the chart ID, as in
	// the Spanner implementation.
	insertDashboardChartSQL = safesql.New(`INSERT INTO DashboardCharts (DashboardId, DashboardChartId, ChartId, GridPosition, CreatedAt)
VALUES ($1, $2, $2, $3, now())`)

	// selectDashboardSQL reads a dashboard and the IDs of its linked charts in one statement, in
	// the column order of dashboardsSelectFields.
	selectDashboardSQL = safesql.Concat(safesql.New(`SELECT d.DashboardId, d.Title, d.Description, d.Owner, d.ChartLayout, `), dashboardChartIDsSQL, safesql.New(`, d.CreatedAt, d.UpdatedAt
FROM Dashboards d
WHERE d.DashboardId = $1`))

	// lockDashboardSQL locks the Dashboards row with FOR NO KEY UPDATE and reads its columns.
	// Child DashboardCharts rows are read in a separate statement after the lock is acquired so
	// that PostgreSQL's READ COMMITTED snapshot for the child query is taken after any concurrent
	// writer holding the parent row lock has committed (EvalPlanQual only re-fetches the locked
	// parent tuple, not correlated subqueries in the target list).
	lockDashboardSQL = safesql.New(`SELECT d.DashboardId, d.Title, d.Description, d.Owner, d.ChartLayout, d.CreatedAt, d.UpdatedAt
FROM Dashboards d
WHERE d.DashboardId = $1
FOR NO KEY UPDATE OF d`)

	selectDashboardChartIDsSQL = safesql.New(`SELECT ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = $1 ORDER BY dc.ChartId)`)

	updateDashboardSQL = safesql.New(`UPDATE Dashboards
SET Title = $2, Description = $3, ChartLayout = $4, UpdatedAt = now()
WHERE DashboardId = $1
RETURNING UpdatedAt`)

	deleteDashboardChartsSQL = safesql.New(`DELETE FROM DashboardCharts WHERE DashboardId = $1`)

	// deleteDashboardSQL deletes a dashboard; its DashboardCharts rows go with it via ON DELETE
	// CASCADE.
	deleteDashboardSQL = safesql.New(`DELETE FROM Dashboards WHERE DashboardId = $1`)
)

// Charts statements.
var (
	// insertChartSQL inserts a chart. Owner is always empty: the Chart proto has no owner field and
	// the Spanner implementation stores the same.
	insertChartSQL = safesql.New(`INSERT INTO Charts (ChartId, Title, Description, Xid, Wid, PlotLayout, Owner, CreatedAt, UpdatedAt)
VALUES ($1, $2, $3, $4, $5, $6, '', now(), now())`)

	// upsertChartPlotSQL writes one plot of a chart. Like the Spanner InsertOrUpdate mutation it
	// lets a later duplicate plot ID in the same request win instead of failing the write.
	upsertChartPlotSQL = safesql.New(`INSERT INTO ChartPlots (ChartId, PlotId, Title, PlotType, PlotSpec)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (ChartId, PlotId) DO UPDATE SET
  Title = EXCLUDED.Title,
  PlotType = EXCLUDED.PlotType,
  PlotSpec = EXCLUDED.PlotSpec`)

	// selectChartSQL reads a chart and the serialized plots of its ChartPlots rows in one statement,
	// in the column order of chartsSelectFields.
	selectChartSQL = safesql.Concat(safesql.New(`SELECT c.ChartId, c.Title, c.Description, c.Xid, c.Wid, c.PlotLayout, c.Owner, `), chartPlotSpecsSQL, safesql.New(`, c.CreatedAt, c.UpdatedAt
FROM Charts c
WHERE c.ChartId = $1`))

	// lockChartSQL locks the Charts row with FOR NO KEY UPDATE and reads its columns.
	// Child ChartPlots rows are read in a separate statement after the lock is acquired so that
	// PostgreSQL's READ COMMITTED snapshot for the child query is taken after any concurrent
	// writer holding the parent row lock has committed.
	lockChartSQL = safesql.New(`SELECT c.ChartId, c.Title, c.Description, c.Xid, c.Wid, c.PlotLayout, c.Owner, c.CreatedAt, c.UpdatedAt
FROM Charts c
WHERE c.ChartId = $1
FOR NO KEY UPDATE OF c`)

	selectChartPlotSpecsSQL = safesql.New(`SELECT ARRAY(SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = $1 ORDER BY cp.PlotId)`)

	// lockChartKeySQL locks a Charts row by key, reporting whether it exists.
	lockChartKeySQL = safesql.New(`SELECT 1 FROM Charts WHERE ChartId = $1 FOR NO KEY UPDATE`)

	// updateChartSQL overwrites every mutable column. Wid is written back unchanged: it is not an
	// updatable field, but the Spanner implementation writes it too.
	updateChartSQL = safesql.New(`UPDATE Charts
SET Title = $2, Description = $3, Wid = $4, PlotLayout = $5, UpdatedAt = now()
WHERE ChartId = $1
RETURNING UpdatedAt`)

	deleteChartPlotsSQL = safesql.New(`DELETE FROM ChartPlots WHERE ChartId = $1`)

	// Charts are referenced by plain (non-cascading) foreign keys from ArtifactCharts and
	// DashboardCharts, so the references are removed before the chart itself.
	deleteArtifactChartReferencesSQL  = safesql.New(`DELETE FROM ArtifactCharts WHERE ChartId = $1`)
	deleteDashboardChartReferencesSQL = safesql.New(`DELETE FROM DashboardCharts WHERE ChartId = $1`)

	// deleteChartSQL deletes a chart; its ChartPlots rows go with it via ON DELETE CASCADE.
	deleteChartSQL = safesql.New(`DELETE FROM Charts WHERE ChartId = $1`)
)

var _ storage.DashboardStore = (*Repository)(nil)

// Repository manages persistence for dashboard and chart entities in PostgreSQL.
type Repository struct {
	// db runs the single-statement reads and writes.
	db pgdb.DBTX
	// pool begins the transactions of the multi-statement writes.
	pool *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL Dashboard repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool), pool: pool}
}

// fetchDashboard reads a dashboard with its chart IDs in a single statement through db. A missing
// dashboard is reported as a NotFound error.
func fetchDashboard(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, dashboardID string) (*dpb.Dashboard, error) {
	row, err := scanDashboardRow(db.QueryRow(ctx, query.String(), dashboardID), len(dashboardsSelectFields))
	if err != nil {
		return nil, translateRowError(err, dashboardEntity, dashboardID)
	}
	return row.toProto()
}

// fetchDashboardForUpdate locks the Dashboards row with FOR NO KEY UPDATE and then reads its
// linked chart IDs under a post-lock READ COMMITTED statement snapshot.
func fetchDashboardForUpdate(ctx context.Context, tx pgx.Tx, dashboardID string) (*dpb.Dashboard, error) {
	var data dashboardRow
	if err := tx.QueryRow(ctx, lockDashboardSQL.String(), dashboardID).Scan(
		&data.ID,
		&data.Title,
		&data.Description,
		&data.Owner,
		&data.ChartLayout,
		&data.CreatedAt,
		&data.UpdatedAt,
	); err != nil {
		return nil, translateRowError(err, dashboardEntity, dashboardID)
	}
	if err := tx.QueryRow(ctx, selectDashboardChartIDsSQL.String(), dashboardID).Scan(&data.ChartIDs); err != nil {
		return nil, translateRowError(err, dashboardEntity, dashboardID)
	}
	return data.toProto()
}

// fetchChart reads a chart with its plots in a single statement through db. A missing chart is
// reported as a NotFound error.
func fetchChart(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, chartID string) (*dpb.Chart, error) {
	row, err := scanChartRow(db.QueryRow(ctx, query.String(), chartID), len(chartsSelectFields))
	if err != nil {
		return nil, translateRowError(err, chartEntity, chartID)
	}
	return row.toProto()
}

// fetchChartForUpdate locks the Charts row with FOR NO KEY UPDATE and then reads its ChartPlots
// rows under a post-lock READ COMMITTED statement snapshot.
func fetchChartForUpdate(ctx context.Context, tx pgx.Tx, chartID string) (*dpb.Chart, error) {
	var (
		data        chartRow
		description pgtype.Text
	)
	if err := tx.QueryRow(ctx, lockChartSQL.String(), chartID).Scan(
		&data.ID,
		&data.Title,
		&description,
		&data.Xid,
		&data.Wid,
		&data.PlotLayout,
		&data.Owner,
		&data.CreatedAt,
		&data.UpdatedAt,
	); err != nil {
		return nil, translateRowError(err, chartEntity, chartID)
	}
	data.Description = description.String
	if err := tx.QueryRow(ctx, selectChartPlotSpecsSQL.String(), chartID).Scan(&data.PlotSpecs); err != nil {
		return nil, translateRowError(err, chartEntity, chartID)
	}
	return data.toProto()
}

// translateRowError maps the error of a single-row read of the identified entity to a storage
// error, naming the entity like the Spanner implementation does when the row does not exist.
func translateRowError(err error, entity, id string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s %s not found: %w", entity, id, grpcerr.ErrNotFound)
	}
	return pgdb.TranslateError(fmt.Errorf("failed to read %s %s: %w", entity, id, err), entity)
}

// insertChart writes a chart and its plots through db.
func insertChart(ctx context.Context, db pgdb.DBTX, chart *chartInsert) error {
	if _, err := db.Exec(ctx, insertChartSQL.String(), chart.ID, chart.Title, chart.Description, chart.Xid, chart.Wid, chart.PlotLayout); err != nil {
		return pgdb.TranslateError(err, chartEntity)
	}
	return insertChartPlots(ctx, db, chart.ID, chart.Plots)
}

// insertChartPlots writes the plots of the identified chart through db.
func insertChartPlots(ctx context.Context, db pgdb.DBTX, chartID string, plots []plotInsert) error {
	for _, plot := range plots {
		if _, err := db.Exec(ctx, upsertChartPlotSQL.String(), chartID, plot.ID, plot.Title, plot.Type, plot.PlotSpec); err != nil {
			return pgdb.TranslateError(err, "chart plot")
		}
	}
	return nil
}

// insertDashboardChartLinks writes the DashboardCharts rows of the identified dashboard through db.
// A link to a chart that does not exist fails the foreign key check and surfaces as a
// FailedPrecondition error.
func insertDashboardChartLinks(ctx context.Context, db pgdb.DBTX, dashboardID string, links []storagedata.DashboardChartLink) error {
	for _, link := range links {
		if _, err := db.Exec(ctx, insertDashboardChartSQL.String(), dashboardID, link.ChartID, link.GridPosition); err != nil {
			return pgdb.TranslateError(err, "dashboard chart link")
		}
	}
	return nil
}
