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

// Package dashboardschema contains the schema constants and query generator TableViews for PostgreSQL Dashboard and Chart tables.
package dashboardschema

import (
	"maps"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

const (
	DashboardsTableName        = "Dashboards"
	DashboardChartsTableName   = "DashboardCharts"
	ChartsTableName            = "Charts"
	ChartPlotsTableName        = "ChartPlots"
	ArtifactChartsTableName    = "ArtifactCharts"
	DashboardIDTableField      = querytools.TableField("dashboard_id")
	DashboardChartIDTableField = querytools.TableField("dashboard_chart_id")
	TitleTableField            = querytools.TableField("title")
	DescriptionTableField      = querytools.TableField("description")
	OwnerTableField            = querytools.TableField("owner")
	ChartIDTableField          = querytools.TableField("chart_id")
	ChartIDsTableField         = querytools.TableField("chart_ids")
	XidTableField              = querytools.TableField("xid")
	WidTableField              = querytools.TableField("wid")
	PlotSpecsTableField        = querytools.TableField("plot_specs")
	ChartLayoutTableField      = querytools.TableField("chart_layout")
	CreatedTableField          = querytools.TableField("created")
	UpdatedTableField          = querytools.TableField("updated")
	PlotLayoutTableField       = querytools.TableField("plot_layout")
)

func chartFields(extra map[querytools.TableField]querytools.Member) map[querytools.TableField]querytools.Member {
	fields := map[querytools.TableField]querytools.Member{
		ChartIDTableField:     {Name: ChartIDTableField, QueryLiteral: safesql.New("c.ChartId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
		TitleTableField:       {Name: TitleTableField, QueryLiteral: safesql.New("c.Title"), Type: filtervisitor.String, Selectable: true},
		DescriptionTableField: {Name: DescriptionTableField, QueryLiteral: safesql.New("c.Description"), Type: filtervisitor.String, Selectable: true, Nullable: true},
		XidTableField:         {Name: XidTableField, QueryLiteral: safesql.New("c.Xid"), Type: filtervisitor.Integer, Selectable: true},
		WidTableField:         {Name: WidTableField, QueryLiteral: safesql.New("c.Wid"), Type: filtervisitor.Integer, Selectable: true},
		PlotLayoutTableField:  {Name: PlotLayoutTableField, QueryLiteral: safesql.New("c.PlotLayout"), Type: filtervisitor.Bytes, Selectable: true, Nullable: true},
		OwnerTableField:       {Name: OwnerTableField, QueryLiteral: safesql.New("c.Owner"), Type: filtervisitor.String, Selectable: true},
		CreatedTableField:     {Name: CreatedTableField, QueryLiteral: safesql.New("c.CreatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
		UpdatedTableField:     {Name: UpdatedTableField, QueryLiteral: safesql.New("c.UpdatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
		PlotSpecsTableField: {
			Name:            PlotSpecsTableField,
			QueryLiteral:    safesql.New("ARRAY(SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = c.ChartId ORDER BY cp.PlotId ASC)"),
			SelectSQLString: safesql.New("ARRAY(SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = c.ChartId ORDER BY cp.PlotId ASC) AS plotspecs"),
			Selectable:      true,
		},
	}
	maps.Copy(fields, extra)
	return fields
}

var (
	dashboardsTableView = querytools.TableView{
		Name:       querytools.TableName(DashboardsTableName),
		NameClause: safesql.New("Dashboards d"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			DashboardIDTableField: {Name: DashboardIDTableField, QueryLiteral: safesql.New("d.DashboardId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
			TitleTableField:       {Name: TitleTableField, QueryLiteral: safesql.New("d.Title"), Type: filtervisitor.String, Selectable: true},
			DescriptionTableField: {Name: DescriptionTableField, QueryLiteral: safesql.New("d.Description"), Type: filtervisitor.String, Selectable: true},
			OwnerTableField:       {Name: OwnerTableField, QueryLiteral: safesql.New("d.Owner"), Type: filtervisitor.String, Selectable: true},
			ChartLayoutTableField: {Name: ChartLayoutTableField, QueryLiteral: safesql.New("d.ChartLayout"), Type: filtervisitor.Bytes, Selectable: true, Nullable: true},
			CreatedTableField:     {Name: CreatedTableField, QueryLiteral: safesql.New("d.CreatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
			UpdatedTableField:     {Name: UpdatedTableField, QueryLiteral: safesql.New("d.UpdatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
			ChartIDsTableField: {
				Name:            ChartIDsTableField,
				QueryLiteral:    safesql.New("ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = d.DashboardId ORDER BY dc.DashboardChartId ASC)"),
				SelectSQLString: safesql.New("ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = d.DashboardId ORDER BY dc.DashboardChartId ASC) AS chartids"),
				Selectable:      true,
			},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{{TableField: DashboardIDTableField, SortOrder: querytools.Asc}},
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: CreatedTableField, SortOrder: querytools.Desc},
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
		},
	}

	chartsTableView = querytools.TableView{
		Name:                            querytools.TableName(ChartsTableName),
		NameClause:                      safesql.New("Charts c"),
		Cost:                            10,
		Fields:                          chartFields(nil),
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{{TableField: ChartIDTableField, SortOrder: querytools.Asc}},
	}

	dashboardChartsJoinedTableView = querytools.TableView{
		Name:       querytools.TableName("DashboardChartsWithCharts"),
		NameClause: safesql.New("DashboardCharts dc JOIN Charts c ON (dc.ChartId = c.ChartId)"),
		Cost:       20,
		Fields: chartFields(map[querytools.TableField]querytools.Member{
			DashboardIDTableField:      {Name: DashboardIDTableField, QueryLiteral: safesql.New("dc.DashboardId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
			DashboardChartIDTableField: {Name: DashboardChartIDTableField, QueryLiteral: safesql.New("dc.DashboardChartId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
		}),
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: DashboardIDTableField, SortOrder: querytools.Asc},
			{TableField: ChartIDTableField, SortOrder: querytools.Asc},
		},
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{{TableField: ChartIDTableField, SortOrder: querytools.Asc}},
	}

	// DashboardTables is the list of TableViews for Dashboard queries.
	DashboardTables = []querytools.TableView{dashboardsTableView}

	// DashboardsSelectFields is the list of fields selected for Dashboard queries.
	DashboardsSelectFields = []querytools.TableField{
		DashboardIDTableField, TitleTableField, DescriptionTableField, OwnerTableField,
		ChartLayoutTableField, CreatedTableField, UpdatedTableField, ChartIDsTableField,
	}

	// DashboardsOrderByFields is the list of fields that can be used for ordering Dashboard queries.
	DashboardsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: CreatedTableField}, {TableField: UpdatedTableField},
		{TableField: DashboardIDTableField}, {TableField: TitleTableField}, {TableField: OwnerTableField},
	}

	// ChartTables is the list of TableViews for Chart queries.
	ChartTables = []querytools.TableView{chartsTableView, dashboardChartsJoinedTableView}

	// ChartsSelectFields is the list of fields selected for Chart list queries.
	ChartsSelectFields = []querytools.TableField{
		ChartIDTableField, TitleTableField, DescriptionTableField, XidTableField,
		WidTableField, PlotLayoutTableField, OwnerTableField, CreatedTableField,
		UpdatedTableField, PlotSpecsTableField,
	}

	// ChartsOrderByFields is the list of fields valid for ordering Chart queries.
	ChartsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: CreatedTableField}, {TableField: UpdatedTableField}, {TableField: ChartIDTableField},
		{TableField: TitleTableField}, {TableField: XidTableField}, {TableField: WidTableField}, {TableField: OwnerTableField},
	}
)
