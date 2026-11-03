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
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	storagedata "xmanager_cloud/internal/storage/data"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/dashboardschema"
	"xmanager_cloud/internal/validator"
)

const (
	dashboardEntityName = "dashboard"
	chartEntityName     = "chart"
)

var (
	_ storage.DashboardStore = (*Repository)(nil)

	insertDashboardSQL             = safesql.New("INSERT INTO Dashboards (DashboardId, Title, Description, Owner, ChartLayout, CreatedAt, UpdatedAt) VALUES (@dashboardid, @title, @description, @owner, @chartlayout, @createdat, @updatedat) ON CONFLICT (DashboardId) DO UPDATE SET Title = EXCLUDED.Title, Description = EXCLUDED.Description, Owner = EXCLUDED.Owner, ChartLayout = EXCLUDED.ChartLayout, CreatedAt = EXCLUDED.CreatedAt, UpdatedAt = EXCLUDED.UpdatedAt")
	insertDashboardChartSQL        = safesql.New("INSERT INTO DashboardCharts (DashboardId, DashboardChartId, ChartId, GridPosition, CreatedAt) VALUES (@dashboardid, @dashboardchartid, @chartid, @gridposition, @createdat) ON CONFLICT (DashboardId, DashboardChartId) DO UPDATE SET ChartId = EXCLUDED.ChartId, GridPosition = EXCLUDED.GridPosition, CreatedAt = EXCLUDED.CreatedAt")
	selectDashboardSQL             = safesql.New("SELECT d.DashboardId, d.Title, d.Description, d.Owner, d.ChartLayout, d.CreatedAt, d.UpdatedAt, ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = d.DashboardId ORDER BY dc.DashboardChartId ASC) AS chartids FROM Dashboards d WHERE d.DashboardId = $1")
	lockDashboardSQL               = safesql.New("SELECT d.DashboardId, d.Title, d.Description, d.Owner, d.ChartLayout, d.CreatedAt, d.UpdatedAt FROM Dashboards d WHERE d.DashboardId = $1 FOR NO KEY UPDATE")
	selectDashboardChartIDsSQL     = safesql.New("SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = $1 ORDER BY dc.DashboardChartId ASC")
	updateDashboardSQL             = safesql.New("UPDATE Dashboards SET Title = @title, Description = @description, ChartLayout = @chartlayout, UpdatedAt = @updatedat WHERE DashboardId = @dashboardid")
	deleteDashboardChartsByDashSQL = safesql.New("DELETE FROM DashboardCharts WHERE DashboardId = $1")
	deleteDashboardSQL             = safesql.New("DELETE FROM Dashboards WHERE DashboardId = $1")

	insertChartSQL                  = safesql.New("INSERT INTO Charts (ChartId, Title, Description, Xid, Wid, PlotLayout, Owner, CreatedAt, UpdatedAt) VALUES (@chartid, @title, @description, @xid, @wid, @plotlayout, @owner, @createdat, @updatedat) ON CONFLICT (ChartId) DO UPDATE SET Title = EXCLUDED.Title, Description = EXCLUDED.Description, Xid = EXCLUDED.Xid, Wid = EXCLUDED.Wid, PlotLayout = EXCLUDED.PlotLayout, Owner = EXCLUDED.Owner, CreatedAt = EXCLUDED.CreatedAt, UpdatedAt = EXCLUDED.UpdatedAt")
	insertChartPlotSQL              = safesql.New("INSERT INTO ChartPlots (ChartId, PlotId, Title, PlotType, PlotSpec) VALUES (@chartid, @plotid, @title, @plottype, @plotspec) ON CONFLICT (ChartId, PlotId) DO UPDATE SET Title = EXCLUDED.Title, PlotType = EXCLUDED.PlotType, PlotSpec = EXCLUDED.PlotSpec")
	selectChartSQL                  = safesql.New("SELECT c.ChartId, c.Title, c.Description, c.Xid, c.Wid, c.PlotLayout, c.Owner, c.CreatedAt, c.UpdatedAt, ARRAY(SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = c.ChartId ORDER BY cp.PlotId ASC) AS plotspecs FROM Charts c WHERE c.ChartId = $1")
	lockChartForNoKeyUpdateSQL      = safesql.New("SELECT c.ChartId, c.Title, c.Description, c.Xid, c.Wid, c.PlotLayout, c.Owner, c.CreatedAt, c.UpdatedAt FROM Charts c WHERE c.ChartId = $1 FOR NO KEY UPDATE")
	selectChartPlotSpecsSQL         = safesql.New("SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = $1 ORDER BY cp.PlotId ASC")
	updateChartSQL                  = safesql.New("UPDATE Charts SET Title = @title, Description = @description, Wid = @wid, PlotLayout = @plotlayout, UpdatedAt = @updatedat WHERE ChartId = @chartid")
	deleteChartPlotsSQL             = safesql.New("DELETE FROM ChartPlots WHERE ChartId = $1")
	lockChartForUpdateSQL           = safesql.New("SELECT ChartId FROM Charts WHERE ChartId = $1 FOR UPDATE")
	deleteArtifactChartsByChartSQL  = safesql.New("DELETE FROM ArtifactCharts WHERE ChartId = $1")
	deleteDashboardChartsByChartSQL = safesql.New("DELETE FROM DashboardCharts WHERE ChartId = $1")
	deleteChartSQL                  = safesql.New("DELETE FROM Charts WHERE ChartId = $1")
)

// Repository manages persistence for dashboard and chart entities in PostgreSQL.
type Repository struct {
	pool               *pgxpool.Pool
	db                 pgdb.DBTX
	dashboardsQueryGen querytools.QueryGenerator
	chartsQueryGen     querytools.QueryGenerator
}

// NewRepository creates a new PostgreSQL Dashboard repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	dashGen, err := querytools.NewQueryGenerator(dashboardschema.DashboardTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		logging.Error(context.Background(), "Failed to initialize dashboard QueryGenerator", "error", err)
	}
	chartGen, err := querytools.NewQueryGenerator(dashboardschema.ChartTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		logging.Error(context.Background(), "Failed to initialize chart QueryGenerator", "error", err)
	}
	return &Repository{pool: pool, db: pgdb.FromPool(pool), dashboardsQueryGen: dashGen, chartsQueryGen: chartGen}
}

// CreateDashboard creates a new dashboard and its associated charts in a single transaction.
func (r *Repository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
	if _, _, err := postgresdata.DashboardProtoToRow(dashboard, time.Time{}); err != nil {
		return err
	}
	for _, chart := range charts {
		if _, _, err := postgresdata.ChartProtoToRow(chart, time.Time{}); err != nil {
			return err
		}
	}
	return pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		now := time.Now().UTC().Truncate(time.Microsecond)
		for _, chart := range charts {
			if err := insertChartInTx(ctx, tx, chart, now); err != nil {
				return err
			}
		}
		return insertDashboardInTx(ctx, tx, dashboard, now)
	})
}

// GetDashboard retrieves a dashboard by its unique ID along with its layout and child chart IDs.
func (r *Repository) GetDashboard(ctx context.Context, dashboardID string) (*dpb.Dashboard, error) {
	if dashboardID == "" {
		return nil, fmt.Errorf("dashboardId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	row, err := pgdb.QueryRow[postgresdata.DashboardRow](ctx, r.db, selectDashboardSQL.String(), dashboardEntityName, dashboardID)
	if err != nil {
		return nil, err
	}
	return postgresdata.DashboardRowToProto(row)
}

// ListDashboards retrieves a paginated and filtered list of dashboards.
func (r *Repository) ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, _ time.Duration) ([]*dpb.Dashboard, string, error) {
	if pageSize <= 0 {
		return nil, "", fmt.Errorf("page_size must be positive: %w", grpcerr.ErrInvalidArgument)
	}
	return pgdb.List(ctx, r.db, dashboardEntityName, pageToken, liststatement.ListParams{
		PageSize:            pageSize,
		Filter:              filter,
		OrderBy:             orderBy,
		QueryGen:            r.dashboardsQueryGen,
		DefaultSelectFields: dashboardschema.DashboardsSelectFields,
		ValidOrderByFields:  dashboardschema.DashboardsOrderByFields,
	}, postgresdata.DashboardRowToProto, storagedata.ExtractDashboardCursor)
}

// UpdateDashboard updates a dashboard with the given update mask.
func (r *Repository) UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fmpb.FieldMask) (*dpb.Dashboard, error) {
	if dashboard == nil || dashboard.GetName() == "" {
		return nil, fmt.Errorf("dashboard and name must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}
	var updatedDashboard *dpb.Dashboard
	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		updatedDashboard = nil
		existing, prevUpdatedAt, err := lockAndFetchDashboard(ctx, tx, dashID)
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
		now := monotonicUpdateTime(prevUpdatedAt)
		updatedDashboard.UpdateTime = timestamppb.New(now)
		return applyDashboardUpdate(ctx, tx, updatedDashboard, plan.UpdateChartIDs, now)
	})
	return updatedDashboard, err
}

// DeleteDashboard deletes a dashboard by its unique ID.
func (r *Repository) DeleteDashboard(ctx context.Context, dashboardID string) error {
	if dashboardID == "" {
		return fmt.Errorf("dashboardId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if r.db == nil {
		return pgdb.WrapStatusError("postgres database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	tag, err := r.db.Exec(ctx, deleteDashboardSQL.String(), dashboardID)
	if err != nil {
		return pgdb.TranslateError(err, dashboardEntityName)
	}
	if tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, dashboardEntityName)
	}
	return nil
}

// CreateChart creates a new chart and its plot specifications in a single transaction.
func (r *Repository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	if _, _, err := postgresdata.ChartProtoToRow(chart, time.Time{}); err != nil {
		return err
	}
	return pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return insertChartInTx(ctx, tx, chart, time.Now().UTC().Truncate(time.Microsecond))
	})
}

// GetChart retrieves a chart by its unique ID along with its plot specifications.
func (r *Repository) GetChart(ctx context.Context, chartID string) (*dpb.Chart, error) {
	if chartID == "" {
		return nil, fmt.Errorf("chartId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	row, err := pgdb.QueryRow[postgresdata.ChartRow](ctx, r.db, selectChartSQL.String(), chartEntityName, chartID)
	if err != nil {
		return nil, err
	}
	return postgresdata.ChartRowToProto(row)
}

// ListCharts retrieves a paginated and filtered list of charts.
func (r *Repository) ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, _ time.Duration) ([]*dpb.Chart, string, error) {
	if pageSize <= 0 {
		return nil, "", fmt.Errorf("page_size must be positive: %w", grpcerr.ErrInvalidArgument)
	}
	combinedFilter, err := storagedata.BuildListChartsFilter(parent, filter)
	if err != nil {
		return nil, "", err
	}
	return pgdb.List(ctx, r.db, chartEntityName, pageToken, liststatement.ListParams{
		PageSize:            pageSize,
		Filter:              combinedFilter,
		OrderBy:             orderBy,
		QueryGen:            r.chartsQueryGen,
		DefaultSelectFields: dashboardschema.ChartsSelectFields,
		ValidOrderByFields:  dashboardschema.ChartsOrderByFields,
	}, postgresdata.ChartRowToProto, storagedata.ExtractChartCursor)
}

// UpdateChart updates a chart with the given update mask.
func (r *Repository) UpdateChart(ctx context.Context, chart *dpb.Chart, updateMask *fmpb.FieldMask) (*dpb.Chart, error) {
	if chart == nil || chart.GetName() == "" {
		return nil, fmt.Errorf("chart and name must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}
	var updatedChart *dpb.Chart
	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		updatedChart = nil
		existing, prevUpdatedAt, err := lockAndFetchChart(ctx, tx, chartID)
		if err != nil {
			return err
		}
		merged, err := storagedata.MergeCharts(existing, chart, updateMask)
		if err != nil {
			return err
		}
		now := monotonicUpdateTime(prevUpdatedAt)
		merged.UpdateTime = timestamppb.New(now)
		if err := applyChartUpdate(ctx, tx, merged, now); err != nil {
			return err
		}
		updatedChart = merged
		return nil
	})
	return updatedChart, err
}

// DeleteChart deletes a chart and its referencing ArtifactCharts and DashboardCharts rows.
func (r *Repository) DeleteChart(ctx context.Context, chartID string) error {
	if chartID == "" {
		return fmt.Errorf("chartId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	return pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := pgdb.QueryRow[postgresdata.ChartRow](ctx, tx, lockChartForUpdateSQL.String(), chartEntityName, chartID); err != nil {
			return err
		}
		for _, sql := range []safesql.TrustedSQLString{deleteArtifactChartsByChartSQL, deleteDashboardChartsByChartSQL, deleteChartSQL} {
			if _, err := tx.Exec(ctx, sql.String(), chartID); err != nil {
				return pgdb.TranslateError(err, chartEntityName)
			}
		}
		return nil
	})
}

func insertDashboardInTx(ctx context.Context, tx pgx.Tx, dashboard *dpb.Dashboard, now time.Time) error {
	row, chartRows, err := postgresdata.DashboardProtoToRow(dashboard, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, insertDashboardSQL.String(), row.GetInsertArgs()); err != nil {
		return pgdb.TranslateError(err, dashboardEntityName)
	}
	return insertChildRowsInTx(ctx, tx, insertDashboardChartSQL, dashboardEntityName, chartRows)
}

func lockAndFetchDashboard(ctx context.Context, tx pgx.Tx, dashID string) (*dpb.Dashboard, time.Time, error) {
	row, dcRows, err := lockParentAndQueryChildren[postgresdata.DashboardRow, postgresdata.DashboardChartRow](
		ctx, tx, dashboardEntityName, dashID, lockDashboardSQL, selectDashboardChartIDsSQL,
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, dc := range dcRows {
		row.ChartIDs = append(row.ChartIDs, dc.ChartID)
	}
	dash, err := postgresdata.DashboardRowToProto(row)
	return dash, row.UpdatedAt, err
}

func applyDashboardUpdate(ctx context.Context, tx pgx.Tx, merged *dpb.Dashboard, updateChartIDs bool, now time.Time) error {
	row, chartRows, err := postgresdata.DashboardProtoToRow(merged, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, updateDashboardSQL.String(), row.GetUpdateArgs()); err != nil {
		return pgdb.TranslateError(err, dashboardEntityName)
	}
	if !updateChartIDs {
		return nil
	}
	if _, err := tx.Exec(ctx, deleteDashboardChartsByDashSQL.String(), row.DashboardID); err != nil {
		return pgdb.TranslateError(err, dashboardEntityName)
	}
	return insertChildRowsInTx(ctx, tx, insertDashboardChartSQL, dashboardEntityName, chartRows)
}

func insertChartInTx(ctx context.Context, tx pgx.Tx, chart *dpb.Chart, now time.Time) error {
	row, plotRows, err := postgresdata.ChartProtoToRow(chart, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, insertChartSQL.String(), row.GetInsertArgs()); err != nil {
		return pgdb.TranslateError(err, chartEntityName)
	}
	return insertChildRowsInTx(ctx, tx, insertChartPlotSQL, chartEntityName, plotRows)
}

func lockAndFetchChart(ctx context.Context, tx pgx.Tx, chartID string) (*dpb.Chart, time.Time, error) {
	row, plotRows, err := lockParentAndQueryChildren[postgresdata.ChartRow, postgresdata.ChartPlotRow](
		ctx, tx, chartEntityName, chartID, lockChartForNoKeyUpdateSQL, selectChartPlotSpecsSQL,
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, p := range plotRows {
		row.PlotSpecs = append(row.PlotSpecs, p.PlotSpec)
	}
	chart, err := postgresdata.ChartRowToProto(row)
	return chart, row.UpdatedAt, err
}

func applyChartUpdate(ctx context.Context, tx pgx.Tx, merged *dpb.Chart, now time.Time) error {
	row, plotRows, err := postgresdata.ChartProtoToRow(merged, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, updateChartSQL.String(), row.GetUpdateArgs()); err != nil {
		return pgdb.TranslateError(err, chartEntityName)
	}
	if _, err := tx.Exec(ctx, deleteChartPlotsSQL.String(), row.ChartID); err != nil {
		return pgdb.TranslateError(err, chartEntityName)
	}
	return insertChildRowsInTx(ctx, tx, insertChartPlotSQL, chartEntityName, plotRows)
}

func insertChildRowsInTx[Row interface{ GetInsertArgs() pgx.StrictNamedArgs }](ctx context.Context, tx pgx.Tx, sql safesql.TrustedSQLString, entity string, rows []Row) error {
	for _, r := range rows {
		if _, err := tx.Exec(ctx, sql.String(), r.GetInsertArgs()); err != nil {
			return pgdb.TranslateError(err, entity)
		}
	}
	return nil
}

func lockParentAndQueryChildren[Parent, Child any](ctx context.Context, tx pgx.Tx, entity, id string, lockSQL, childSQL safesql.TrustedSQLString) (Parent, []Child, error) {
	var zero Parent
	parent, err := pgdb.QueryRow[Parent](ctx, tx, lockSQL.String(), entity, id)
	if err != nil {
		return zero, nil, err
	}
	children, err := pgdb.QueryRows[Child](ctx, tx, childSQL.String(), entity, id)
	return parent, children, err
}

func monotonicUpdateTime(prevUpdatedAt time.Time) time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(prevUpdatedAt) {
		return prevUpdatedAt.Add(time.Microsecond)
	}
	return now
}
