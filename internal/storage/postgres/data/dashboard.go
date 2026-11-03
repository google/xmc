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

package postgresdata

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// DashboardRow represents a row in the PostgreSQL Dashboards table.
type DashboardRow struct {
	DashboardID string    `db:"dashboardid"`
	Title       string    `db:"title"`
	Description string    `db:"description"`
	Owner       string    `db:"owner"`
	ChartLayout []byte    `db:"chartlayout"`
	CreatedAt   time.Time `db:"createdat"`
	UpdatedAt   time.Time `db:"updatedat"`
	ChartIDs    []string  `db:"chartids"`
}

// GetInsertArgs returns the strict named arguments required by the Dashboards INSERT statement.
func (r DashboardRow) GetInsertArgs() pgx.StrictNamedArgs {
	args := r.GetUpdateArgs()
	args["owner"] = r.Owner
	args["createdat"] = r.CreatedAt
	return args
}

// GetUpdateArgs returns the strict named arguments required by the Dashboards UPDATE statement.
func (r DashboardRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"dashboardid": r.DashboardID, "title": r.Title, "description": r.Description,
		"chartlayout": r.ChartLayout, "updatedat": r.UpdatedAt,
	}
}

// DashboardChartRow represents a row in the PostgreSQL DashboardCharts table.
type DashboardChartRow struct {
	DashboardID      string    `db:"dashboardid"`
	DashboardChartID string    `db:"dashboardchartid"`
	ChartID          string    `db:"chartid"`
	GridPosition     []byte    `db:"gridposition"`
	CreatedAt        time.Time `db:"createdat"`
}

// GetInsertArgs returns the strict named arguments required by the DashboardCharts INSERT statement.
func (r DashboardChartRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"dashboardid": r.DashboardID, "dashboardchartid": r.DashboardChartID,
		"chartid": r.ChartID, "gridposition": r.GridPosition, "createdat": r.CreatedAt,
	}
}

// DashboardRowToProto converts a scanned PostgreSQL DashboardRow into a dpb.Dashboard proto.
func DashboardRowToProto(row DashboardRow) (*dpb.Dashboard, error) {
	if row.DashboardID == "" {
		return nil, fmt.Errorf("dashboard row missing DashboardId")
	}
	var layout *dpb.LayoutSpec
	if row.ChartLayout != nil {
		var err error
		if layout, err = UnmarshalProto[dpb.LayoutSpec](row.ChartLayout); err != nil {
			return nil, fmt.Errorf("failed to unmarshal dashboard layout: %w", err)
		}
	}
	return &dpb.Dashboard{
		Name: validator.ConstructDashboardName(row.DashboardID), Title: row.Title,
		Description: row.Description, Owner: row.Owner, ChartIds: storagedata.SortChartIDs(row.ChartIDs),
		ChartLayout: layout, CreateTime: timestamppb.New(row.CreatedAt), UpdateTime: timestamppb.New(row.UpdatedAt),
	}, nil
}

// DashboardProtoToRow converts a dpb.Dashboard proto and timestamp into a DashboardRow and child DashboardChartRows.
func DashboardProtoToRow(dashboard *dpb.Dashboard, now time.Time) (DashboardRow, []DashboardChartRow, error) {
	if dashboard == nil {
		return DashboardRow{}, nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return DashboardRow{}, nil, fmt.Errorf("invalid dashboard name: %w", err)
	}
	layoutBytes, err := MarshalNullableProto(dashboard.GetChartLayout())
	if err != nil {
		return DashboardRow{}, nil, fmt.Errorf("failed to marshal chart layout: %w", err)
	}
	links, err := storagedata.BuildDashboardChartLinks(dashboard)
	if err != nil {
		return DashboardRow{}, nil, err
	}
	chartRows := make([]DashboardChartRow, 0, len(links))
	for _, link := range links {
		chartRows = append(chartRows, DashboardChartRow{
			DashboardID: dashID, DashboardChartID: link.ChartID, ChartID: link.ChartID,
			GridPosition: link.GridPosition, CreatedAt: now,
		})
	}
	return DashboardRow{
		DashboardID: dashID, Title: dashboard.GetTitle(), Description: dashboard.GetDescription(),
		Owner: dashboard.GetOwner(), ChartLayout: layoutBytes, CreatedAt: now, UpdatedAt: now,
		ChartIDs: storagedata.SortChartIDs(dashboard.GetChartIds()),
	}, chartRows, nil
}

// ChartRow represents a row in the PostgreSQL Charts table.
type ChartRow struct {
	ChartID     string      `db:"chartid"`
	Title       string      `db:"title"`
	Description pgtype.Text `db:"description"`
	Xid         int64       `db:"xid"`
	Wid         int64       `db:"wid"`
	PlotLayout  []byte      `db:"plotlayout"`
	Owner       string      `db:"owner"`
	CreatedAt   time.Time   `db:"createdat"`
	UpdatedAt   time.Time   `db:"updatedat"`
	PlotSpecs   [][]byte    `db:"plotspecs"`
}

// GetInsertArgs returns the strict named arguments required by the Charts INSERT statement.
func (r ChartRow) GetInsertArgs() pgx.StrictNamedArgs {
	args := r.GetUpdateArgs()
	args["xid"] = r.Xid
	args["owner"] = r.Owner
	args["createdat"] = r.CreatedAt
	return args
}

// GetUpdateArgs returns the strict named arguments required by the Charts UPDATE statement.
func (r ChartRow) GetUpdateArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"chartid": r.ChartID, "title": r.Title, "description": r.Description,
		"wid": r.Wid, "plotlayout": r.PlotLayout, "updatedat": r.UpdatedAt,
	}
}

// ChartPlotRow represents a row in the PostgreSQL ChartPlots table.
type ChartPlotRow struct {
	ChartID  string `db:"chartid"`
	PlotID   string `db:"plotid"`
	Title    string `db:"title"`
	PlotType int64  `db:"plottype"`
	PlotSpec []byte `db:"plotspec"`
}

// GetInsertArgs returns the strict named arguments required by the ChartPlots INSERT statement.
func (r ChartPlotRow) GetInsertArgs() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"chartid": r.ChartID, "plotid": r.PlotID, "title": r.Title,
		"plottype": r.PlotType, "plotspec": r.PlotSpec,
	}
}

// ChartRowToProto converts a scanned PostgreSQL ChartRow into a dpb.Chart proto.
func ChartRowToProto(row ChartRow) (*dpb.Chart, error) {
	if row.ChartID == "" {
		return nil, fmt.Errorf("chart row missing ChartId")
	}
	var layout *dpb.LayoutSpec
	if row.PlotLayout != nil {
		var err error
		if layout, err = UnmarshalProto[dpb.LayoutSpec](row.PlotLayout); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chart layout: %w", err)
		}
	}
	var plots []*dpb.PlotSpec
	for _, specBytes := range row.PlotSpecs {
		plot, err := storagedata.UnmarshalPlotSpec(row.ChartID, specBytes)
		if err != nil {
			return nil, err
		}
		plots = append(plots, plot)
	}
	name := validator.ConstructRootChartName(row.ChartID)
	if row.Xid > 0 {
		name = validator.ConstructWorkUnitChartName(row.Xid, row.Wid, row.ChartID)
	}
	return &dpb.Chart{
		Name: name, Title: row.Title, Description: row.Description.String,
		Xid: row.Xid, Wid: row.Wid, Plots: plots, PlotLayout: layout,
		CreateTime: timestamppb.New(row.CreatedAt), UpdateTime: timestamppb.New(row.UpdatedAt),
	}, nil
}

// ChartProtoToRow converts a dpb.Chart proto and timestamp into a ChartRow and child ChartPlotRows.
func ChartProtoToRow(chart *dpb.Chart, now time.Time) (ChartRow, []ChartPlotRow, error) {
	if chart == nil {
		return ChartRow{}, nil, fmt.Errorf("chart proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	xid, wid, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return ChartRow{}, nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}
	layoutBytes, err := MarshalNullableProto(chart.GetPlotLayout())
	if err != nil {
		return ChartRow{}, nil, fmt.Errorf("failed to marshal plot layout: %w", err)
	}
	plotRows := make([]ChartPlotRow, 0, len(chart.GetPlots()))
	for _, plot := range chart.GetPlots() {
		if plot == nil {
			continue
		}
		specBytes, err := MarshalProto(plot)
		if err != nil {
			return ChartRow{}, nil, fmt.Errorf("failed to marshal plot spec: %w", err)
		}
		plotRows = append(plotRows, ChartPlotRow{
			ChartID: chartID, PlotID: plot.GetPlotId(), Title: plot.GetTitle(),
			PlotType: int64(plot.GetType()), PlotSpec: specBytes,
		})
	}
	return ChartRow{
		ChartID: chartID, Title: chart.GetTitle(),
		Description: pgtype.Text{String: chart.GetDescription(), Valid: true},
		Xid:         max(xid, chart.GetXid()), Wid: max(wid, chart.GetWid()),
		PlotLayout: layoutBytes, Owner: "", CreatedAt: now, UpdatedAt: now,
	}, plotRows, nil
}
