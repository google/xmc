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

// Package dashboardschema contains the schema constants and query generator TableViews for Dashboard Spanner tables.
package dashboardschema

import (
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	DashboardsTableName      = "Dashboards"
	DashboardChartsTableName = "DashboardCharts"
	ChartsTableName          = "Charts"
	ChartPlotsTableName      = "ChartPlots"
	ArtifactChartsTableName  = "ArtifactCharts"

	DashboardIDTableField      = querytools.TableField("dashboard_id")
	DashboardChartIDTableField = querytools.TableField("dashboard_chart_id")
	TitleTableField            = querytools.TableField("title")
	DescriptionTableField      = querytools.TableField("description")
	OwnerTableField            = querytools.TableField("owner")
	ChartIDTableField          = querytools.TableField("chart_id")
	XidTableField              = querytools.TableField("xid")
	WidTableField              = querytools.TableField("wid")
	PlotIDTableField           = querytools.TableField("plot_id")
	PlotSpecTableField         = querytools.TableField("plot_spec")
	ChartLayoutTableField      = querytools.TableField("chart_layout")
	CreatedTableField          = querytools.TableField("created")
	UpdatedTableField          = querytools.TableField("updated")
	PlotLayoutTableField       = querytools.TableField("plot_layout")
	WorkUnitIDTableField       = querytools.TableField("work_unit_id")
	ArtifactIDTableField       = querytools.TableField("artifact_id")
	ArtifactChartIDTableField  = querytools.TableField("artifact_chart_id")
	BitReversedXidTableField   = querytools.TableField("bit_reversed_xid")
)

var (
	FetchedDashboardColumns = []string{
		"DashboardId",
		"Title",
		"Description",
		"Owner",
		"ChartLayout",
		"CreatedAt",
		"UpdatedAt",
	}

	FetchedChartColumns = []string{
		"ChartId",
		"Title",
		"Description",
		"Xid",
		"Wid",
		"PlotLayout",
		"Owner",
		"CreatedAt",
		"UpdatedAt",
	}

	dashboardsTableView = querytools.TableView{
		Name:       querytools.TableName(DashboardsTableName),
		NameClause: safesql.New("Dashboards d"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			DashboardIDTableField: {
				Name:         DashboardIDTableField,
				QueryLiteral: safesql.New("d.DashboardId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			TitleTableField: {
				Name:         TitleTableField,
				QueryLiteral: safesql.New("d.Title"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			DescriptionTableField: {
				Name:         DescriptionTableField,
				QueryLiteral: safesql.New("d.Description"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			OwnerTableField: {
				Name:         OwnerTableField,
				QueryLiteral: safesql.New("d.Owner"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			ChartLayoutTableField: {
				Name:         ChartLayoutTableField,
				QueryLiteral: safesql.New("d.ChartLayout"),
				Selectable:   true,
				Indexed:      false,
			},
			CreatedTableField: {
				Name:         CreatedTableField,
				QueryLiteral: safesql.New("d.CreatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			UpdatedTableField: {
				Name:         UpdatedTableField,
				QueryLiteral: safesql.New("d.UpdatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
		},
		// Default ListDashboards order: newest first; dashboard_id is a unique tie-breaker so
		// page-token cursors are deterministic.
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: CreatedTableField, SortOrder: querytools.Desc},
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
		},
	}

	chartsTableView = querytools.TableView{
		Name:       querytools.TableName(ChartsTableName),
		NameClause: safesql.New("Charts c"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			ChartIDTableField: {
				Name:         ChartIDTableField,
				QueryLiteral: safesql.New("c.ChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			TitleTableField: {
				Name:         TitleTableField,
				QueryLiteral: safesql.New("c.Title"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			DescriptionTableField: {
				Name:         DescriptionTableField,
				QueryLiteral: safesql.New("c.Description"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			XidTableField: {
				Name:         XidTableField,
				QueryLiteral: safesql.New("c.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			WidTableField: {
				Name:         WidTableField,
				QueryLiteral: safesql.New("c.Wid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			PlotLayoutTableField: {
				Name:         PlotLayoutTableField,
				QueryLiteral: safesql.New("c.PlotLayout"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
				Indexed:      false,
			},
			OwnerTableField: {
				Name:         OwnerTableField,
				QueryLiteral: safesql.New("c.Owner"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			CreatedTableField: {
				Name:         CreatedTableField,
				QueryLiteral: safesql.New("c.CreatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			UpdatedTableField: {
				Name:         UpdatedTableField,
				QueryLiteral: safesql.New("c.UpdatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: ChartIDTableField, SortOrder: querytools.Asc},
		},
	}

	dashboardChartsTableView = querytools.TableView{
		Name:       querytools.TableName(DashboardChartsTableName),
		NameClause: safesql.New("DashboardCharts dc"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			DashboardIDTableField: {
				Name:         DashboardIDTableField,
				QueryLiteral: safesql.New("dc.DashboardId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			DashboardChartIDTableField: {
				Name:         DashboardChartIDTableField,
				QueryLiteral: safesql.New("dc.DashboardChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ChartIDTableField: {
				Name:         ChartIDTableField,
				QueryLiteral: safesql.New("dc.ChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
		},
	}

	chartPlotsTableView = querytools.TableView{
		Name:       querytools.TableName(ChartPlotsTableName),
		NameClause: safesql.New("ChartPlots cp"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			// Plots are fetched for a whole page of charts at once, so the chart_id disjunction is
			// collapsed into a single IN UNNEST clause instead of one OR term per chart.
			ChartIDTableField: {
				Name:                   ChartIDTableField,
				QueryLiteral:           safesql.New("cp.ChartId"),
				Type:                   filtervisitor.String,
				Selectable:             true,
				Indexed:                true,
				DisjunctionOptimizable: true,
			},
			PlotIDTableField: {
				Name:         PlotIDTableField,
				QueryLiteral: safesql.New("cp.PlotId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			PlotSpecTableField: {
				Name:         PlotSpecTableField,
				QueryLiteral: safesql.New("cp.PlotSpec"),
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: ChartIDTableField, SortOrder: querytools.Asc},
		},
	}

	artifactChartsTableView = querytools.TableView{
		Name:       querytools.TableName(ArtifactChartsTableName),
		NameClause: safesql.New("ArtifactCharts ac"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			BitReversedXidTableField: {
				Name:         BitReversedXidTableField,
				QueryLiteral: safesql.New("ac.BitReversedXid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			WorkUnitIDTableField: {
				Name:         WorkUnitIDTableField,
				QueryLiteral: safesql.New("ac.WorkUnitId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			ArtifactIDTableField: {
				Name:         ArtifactIDTableField,
				QueryLiteral: safesql.New("ac.ArtifactId"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
			},
			ArtifactChartIDTableField: {
				Name:         ArtifactChartIDTableField,
				QueryLiteral: safesql.New("ac.ArtifactChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ChartIDTableField: {
				Name:         ChartIDTableField,
				QueryLiteral: safesql.New("ac.ChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: BitReversedXidTableField, SortOrder: querytools.Asc},
		},
	}

	dashboardChartsJoinedTableView = querytools.TableView{
		Name:       querytools.TableName("DashboardChartsWithCharts"),
		NameClause: safesql.New("DashboardCharts dc JOIN Charts c ON (dc.ChartId = c.ChartId)"),
		Cost:       20,
		Fields: map[querytools.TableField]querytools.Member{
			DashboardIDTableField: {
				Name:         DashboardIDTableField,
				QueryLiteral: safesql.New("dc.DashboardId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			DashboardChartIDTableField: {
				Name:         DashboardChartIDTableField,
				QueryLiteral: safesql.New("dc.DashboardChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ChartIDTableField: {
				Name:         ChartIDTableField,
				QueryLiteral: safesql.New("c.ChartId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			TitleTableField: {
				Name:         TitleTableField,
				QueryLiteral: safesql.New("c.Title"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			DescriptionTableField: {
				Name:         DescriptionTableField,
				QueryLiteral: safesql.New("c.Description"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			XidTableField: {
				Name:         XidTableField,
				QueryLiteral: safesql.New("c.Xid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			WidTableField: {
				Name:         WidTableField,
				QueryLiteral: safesql.New("c.Wid"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      false,
			},
			PlotLayoutTableField: {
				Name:         PlotLayoutTableField,
				QueryLiteral: safesql.New("c.PlotLayout"),
				Type:         filtervisitor.Bytes,
				Selectable:   true,
				Indexed:      false,
			},
			OwnerTableField: {
				Name:         OwnerTableField,
				QueryLiteral: safesql.New("c.Owner"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      false,
			},
			CreatedTableField: {
				Name:         CreatedTableField,
				QueryLiteral: safesql.New("c.CreatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
			UpdatedTableField: {
				Name:         UpdatedTableField,
				QueryLiteral: safesql.New("c.UpdatedAt"),
				Type:         filtervisitor.Timestamp,
				Selectable:   true,
				Indexed:      false,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
			{TableField: ChartIDTableField, SortOrder: querytools.Asc},
		},
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: ChartIDTableField, SortOrder: querytools.Asc},
		},
	}

	// DashboardTables is the list of TableViews for Dashboard queries.
	DashboardTables = []querytools.TableView{dashboardsTableView}

	// DashboardsSelectFields is the list of fields selected for Dashboard queries.
	DashboardsSelectFields = []querytools.TableField{
		DashboardIDTableField,
		TitleTableField,
		DescriptionTableField,
		OwnerTableField,
		ChartLayoutTableField,
		CreatedTableField,
		UpdatedTableField,
	}

	// DashboardsOrderByFields is the list of fields that can be used for ordering Dashboard queries.
	DashboardsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: CreatedTableField},
		{TableField: UpdatedTableField},
		{TableField: DashboardIDTableField},
		{TableField: TitleTableField},
		{TableField: OwnerTableField},
	}

	// ChartTables is the list of TableViews for Chart queries.
	ChartTables = []querytools.TableView{chartsTableView, dashboardChartsJoinedTableView}

	// DashboardChartsTables is the list of TableViews for DashboardCharts queries.
	DashboardChartsTables = []querytools.TableView{dashboardChartsTableView}

	// ChartPlotsTables is the list of TableViews for ChartPlots queries.
	ChartPlotsTables = []querytools.TableView{chartPlotsTableView}

	// ArtifactChartsTables is the list of TableViews for ArtifactCharts queries.
	ArtifactChartsTables = []querytools.TableView{artifactChartsTableView}

	// ChartsSelectFields is the list of fields selected for Chart list queries.
	ChartsSelectFields = []querytools.TableField{
		ChartIDTableField,
		TitleTableField,
		DescriptionTableField,
		XidTableField,
		WidTableField,
		PlotLayoutTableField,
		OwnerTableField,
		CreatedTableField,
		UpdatedTableField,
	}

	// ChartsOrderByFields is the list of fields valid for ordering Chart queries.
	ChartsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: CreatedTableField},
		{TableField: UpdatedTableField},
		{TableField: ChartIDTableField},
		{TableField: TitleTableField},
		{TableField: XidTableField},
		{TableField: WidTableField},
		{TableField: OwnerTableField},
	}
)
