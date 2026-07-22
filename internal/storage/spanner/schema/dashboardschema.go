// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package dashboardschema contains the schema constants and query generator TableViews for Dashboard Spanner tables.
package dashboardschema

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"xmanager_cloud/query_generator/querytools"
)

const (
	DashboardsTableName      = "Dashboards"
	DashboardChartsTableName = "DashboardCharts"
	ChartsTableName          = "Charts"
	ChartPlotsTableName      = "ChartPlots"
	ArtifactChartsTableName  = "ArtifactCharts"

	DashboardIDTableField = querytools.TableField("dashboard_id")
	TitleTableField       = querytools.TableField("title")
	DescriptionTableField = querytools.TableField("description")
	OwnerTableField       = querytools.TableField("owner")
	ChartIDTableField     = querytools.TableField("chart_id")
	XidTableField         = querytools.TableField("xid")
	PlotIDTableField      = querytools.TableField("plot_id")
	CreatedTableField     = querytools.TableField("created")
	UpdatedTableField     = querytools.TableField("updated")
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
		"Xid",
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
			XidTableField: {
				Name:         XidTableField,
				QueryLiteral: safesql.New("c.Xid"),
				Type:         filtervisitor.Integer,
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

	// DashboardTables is the list of TableViews for Dashboard queries.
	DashboardTables = []querytools.TableView{dashboardsTableView}

	// ChartTables is the list of TableViews for Chart queries.
	ChartTables = []querytools.TableView{chartsTableView}
)
