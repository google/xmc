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
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	storagedata "xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

// dashboardRow is a Dashboards row together with the ChartId column of its DashboardCharts rows,
// read in the column order of dashboardsSelectFields.
type dashboardRow struct {
	ID          string
	Title       string
	Description string
	Owner       string
	ChartLayout []byte
	ChartIDs    []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// chartRow is a Charts row together with the PlotSpec column of its ChartPlots rows, read in the
// column order of chartsSelectFields.
type chartRow struct {
	ID          string
	Title       string
	Description string
	Xid         int64
	Wid         int64
	PlotLayout  []byte
	Owner       string
	PlotSpecs   [][]byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// scanDashboardRow decodes a row selected with dashboardsSelectFields. Columns beyond those fields,
// which the query generator may append for page tokens, are discarded.
func scanDashboardRow(row pgx.Row, numColumns int) (*dashboardRow, error) {
	var data dashboardRow
	dest := []any{&data.ID, &data.Title, &data.Description, &data.Owner, &data.ChartLayout, &data.ChartIDs, &data.CreatedAt, &data.UpdatedAt}
	if err := row.Scan(padScanDest(dest, numColumns)...); err != nil {
		return nil, err
	}
	return &data, nil
}

// scanChartRow decodes a row selected with chartsSelectFields. Columns beyond those fields, which
// the query generator may append for page tokens, are discarded. A NULL Description reads as "".
func scanChartRow(row pgx.Row, numColumns int) (*chartRow, error) {
	var (
		data        chartRow
		description pgtype.Text
	)
	dest := []any{&data.ID, &data.Title, &description, &data.Xid, &data.Wid, &data.PlotLayout, &data.Owner, &data.PlotSpecs, &data.CreatedAt, &data.UpdatedAt}
	if err := row.Scan(padScanDest(dest, numColumns)...); err != nil {
		return nil, err
	}
	data.Description = description.String
	return &data, nil
}

// padScanDest appends throw-away destinations to dest until it has one per selected column, since
// pgx requires exactly one destination per column.
func padScanDest(dest []any, numColumns int) []any {
	for len(dest) < numColumns {
		dest = append(dest, new(any))
	}
	return dest
}

// toProto converts the row into a Dashboard, sorting its chart IDs canonically.
func (r *dashboardRow) toProto() (*dpb.Dashboard, error) {
	if r.ID == "" {
		return nil, errors.New("dashboard row missing DashboardId")
	}
	layout, err := unmarshalLayout(r.ChartLayout, "dashboard")
	if err != nil {
		return nil, err
	}
	return &dpb.Dashboard{
		Name:        validator.ConstructDashboardName(r.ID),
		Title:       r.Title,
		Description: r.Description,
		Owner:       r.Owner,
		ChartIds:    storagedata.SortChartIDs(r.ChartIDs),
		ChartLayout: layout,
		CreateTime:  timestamppb.New(r.CreatedAt),
		UpdateTime:  timestamppb.New(r.UpdatedAt),
	}, nil
}

// toProto converts the row into a Chart. Charts that belong to a work unit are named under it;
// all others get the root "charts/{chart_id}" name.
func (r *chartRow) toProto() (*dpb.Chart, error) {
	if r.ID == "" {
		return nil, errors.New("chart row missing ChartId")
	}
	layout, err := unmarshalLayout(r.PlotLayout, "chart")
	if err != nil {
		return nil, err
	}
	var plots []*dpb.PlotSpec
	for _, plotSpecBytes := range r.PlotSpecs {
		plot, err := storagedata.UnmarshalPlotSpec(r.ID, plotSpecBytes)
		if err != nil {
			return nil, err
		}
		plots = append(plots, plot)
	}

	name := validator.ConstructRootChartName(r.ID)
	if r.Xid > 0 {
		name = validator.ConstructWorkUnitChartName(r.Xid, r.Wid, r.ID)
	}
	return &dpb.Chart{
		Name:        name,
		Title:       r.Title,
		Description: r.Description,
		Xid:         r.Xid,
		Wid:         r.Wid,
		Plots:       plots,
		PlotLayout:  layout,
		CreateTime:  timestamppb.New(r.CreatedAt),
		UpdateTime:  timestamppb.New(r.UpdatedAt),
	}, nil
}

// marshalLayout serializes layout for a BYTEA column, storing a nil layout as NULL.
func marshalLayout(layout *dpb.LayoutSpec, kind string) ([]byte, error) {
	if layout == nil {
		return nil, nil
	}
	layoutBytes, err := proto.Marshal(layout)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s layout: %w", kind, err)
	}
	return layoutBytes, nil
}

// unmarshalLayout decodes a BYTEA layout column, reading NULL and empty values as no layout.
func unmarshalLayout(layoutBytes []byte, kind string) (*dpb.LayoutSpec, error) {
	if len(layoutBytes) == 0 {
		return nil, nil
	}
	layout := &dpb.LayoutSpec{}
	if err := proto.Unmarshal(layoutBytes, layout); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s layout: %w", kind, err)
	}
	return layout, nil
}

// chartInsert holds the validated, serialized column values of a chart and its plots, ready to be
// written. Building it before the transaction starts keeps validation out of the retried callback.
type chartInsert struct {
	ID          string
	Title       string
	Description string
	Xid         int64
	Wid         int64
	PlotLayout  []byte
	Plots       []plotInsert
}

// plotInsert holds the column values of one ChartPlots row.
type plotInsert struct {
	ID       string
	Title    string
	Type     int64
	PlotSpec []byte
}

// newChartInsert validates chart and serializes it for insertion. A chart whose xid or wid is unset
// takes them from its resource name, as the Spanner implementation does.
func newChartInsert(chart *dpb.Chart) (*chartInsert, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	xid, wid, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}

	chartXid := chart.GetXid()
	if chartXid == 0 && xid > 0 {
		chartXid = xid
	}
	chartWid := chart.GetWid()
	if chartWid == 0 && wid > 0 {
		chartWid = wid
	}

	layoutBytes, err := marshalLayout(chart.GetPlotLayout(), "plot")
	if err != nil {
		return nil, err
	}
	plots, err := newPlotInserts(chart.GetPlots())
	if err != nil {
		return nil, err
	}
	return &chartInsert{
		ID:          chartID,
		Title:       chart.GetTitle(),
		Description: chart.GetDescription(),
		Xid:         chartXid,
		Wid:         chartWid,
		PlotLayout:  layoutBytes,
		Plots:       plots,
	}, nil
}

// newPlotInserts serializes plots for insertion, skipping nil entries.
func newPlotInserts(plots []*dpb.PlotSpec) ([]plotInsert, error) {
	inserts := make([]plotInsert, 0, len(plots))
	for _, plot := range plots {
		if plot == nil {
			continue
		}
		plotSpecBytes, err := proto.Marshal(plot)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal plot spec: %w", err)
		}
		inserts = append(inserts, plotInsert{
			ID:       plot.GetPlotId(),
			Title:    plot.GetTitle(),
			Type:     int64(plot.GetType()),
			PlotSpec: plotSpecBytes,
		})
	}
	return inserts, nil
}

// dashboardInsert holds the validated, serialized column values of a dashboard and its chart links,
// ready to be written.
type dashboardInsert struct {
	ID          string
	Title       string
	Description string
	Owner       string
	ChartLayout []byte
	Links       []storagedata.DashboardChartLink
}

// newDashboardInsert validates dashboard and serializes it for insertion.
func newDashboardInsert(dashboard *dpb.Dashboard) (*dashboardInsert, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}
	layoutBytes, err := marshalLayout(dashboard.GetChartLayout(), "chart")
	if err != nil {
		return nil, err
	}
	links, err := storagedata.BuildDashboardChartLinks(dashboard)
	if err != nil {
		return nil, err
	}
	return &dashboardInsert{
		ID:          dashID,
		Title:       dashboard.GetTitle(),
		Description: dashboard.GetDescription(),
		Owner:       dashboard.GetOwner(),
		ChartLayout: layoutBytes,
		Links:       links,
	}, nil
}
