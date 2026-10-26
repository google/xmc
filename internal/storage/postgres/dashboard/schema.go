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
	"fmt"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
)

// Table names of the PostgreSQL dashboard schema, as declared in schema/ess-schema.sql.
const (
	dashboardsTableName      = "Dashboards"
	dashboardChartsTableName = "DashboardCharts"
	chartsTableName          = "Charts"
	chartPlotsTableName      = "ChartPlots"
	artifactChartsTableName  = "ArtifactCharts"
)

// Field names that user filters and order-by clauses refer to. They match the Spanner
// dashboardschema so that filters, order-by strings and page tokens are interchangeable between
// the two backends.
const (
	dashboardIDTableField      = querytools.TableField("dashboard_id")
	dashboardChartIDTableField = querytools.TableField("dashboard_chart_id")
	titleTableField            = querytools.TableField("title")
	descriptionTableField      = querytools.TableField("description")
	ownerTableField            = querytools.TableField("owner")
	chartIDTableField          = querytools.TableField("chart_id")
	xidTableField              = querytools.TableField("xid")
	widTableField              = querytools.TableField("wid")
	chartLayoutTableField      = querytools.TableField("chart_layout")
	plotLayoutTableField       = querytools.TableField("plot_layout")
	createdTableField          = querytools.TableField("created")
	updatedTableField          = querytools.TableField("updated")
	// chartIDsTableField selects the IDs of the charts linked to a dashboard, read from the
	// DashboardCharts child table in the same statement as the dashboard row.
	chartIDsTableField = querytools.TableField("chart_ids")
	// plotSpecsTableField selects the serialized PlotSpec protos of a chart, read from the ChartPlots
	// child table in the same statement as the chart row.
	plotSpecsTableField = querytools.TableField("plot_specs")
)

// Child-row aggregates selected alongside their parent row. Reading the children in the same
// statement keeps a listing consistent without a second query or a REPEATABLE READ transaction,
// and keeps its cost independent of the page size. Both are ordered by their primary key so that
// results are deterministic.
var (
	dashboardChartIDsSQL = safesql.New("ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = d.DashboardId ORDER BY dc.ChartId)")
	chartPlotSpecsSQL    = safesql.New("ARRAY(SELECT cp.PlotSpec FROM ChartPlots cp WHERE cp.ChartId = c.ChartId ORDER BY cp.PlotId)")
)

var (
	dashboardsTableView = querytools.TableView{
		Name:       querytools.TableName(dashboardsTableName),
		NameClause: safesql.New("Dashboards d"),
		Cost:       10,
		Fields: map[querytools.TableField]querytools.Member{
			dashboardIDTableField: {Name: dashboardIDTableField, QueryLiteral: safesql.New("d.DashboardId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
			titleTableField:       {Name: titleTableField, QueryLiteral: safesql.New("d.Title"), Type: filtervisitor.String, Selectable: true},
			descriptionTableField: {Name: descriptionTableField, QueryLiteral: safesql.New("d.Description"), Type: filtervisitor.String, Selectable: true},
			ownerTableField:       {Name: ownerTableField, QueryLiteral: safesql.New("d.Owner"), Type: filtervisitor.String, Selectable: true},
			chartLayoutTableField: {Name: chartLayoutTableField, QueryLiteral: safesql.New("d.ChartLayout"), Selectable: true},
			chartIDsTableField:    {Name: chartIDsTableField, QueryLiteral: dashboardChartIDsSQL, Selectable: true, Derived: true},
			createdTableField:     {Name: createdTableField, QueryLiteral: safesql.New("d.CreatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
			updatedTableField:     {Name: updatedTableField, QueryLiteral: safesql.New("d.UpdatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
		},
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: dashboardIDTableField, SortOrder: querytools.Asc},
		},
		// Default ListDashboards order: newest first; dashboard_id is a unique tie-breaker so
		// page-token cursors are deterministic.
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: createdTableField, SortOrder: querytools.Desc},
			{TableField: dashboardIDTableField, SortOrder: querytools.Asc},
		},
	}

	// chartFields are the Charts columns shared by the plain and the dashboard-joined chart views.
	chartFields = map[querytools.TableField]querytools.Member{
		chartIDTableField:     {Name: chartIDTableField, QueryLiteral: safesql.New("c.ChartId"), Type: filtervisitor.String, Selectable: true, Indexed: true},
		titleTableField:       {Name: titleTableField, QueryLiteral: safesql.New("c.Title"), Type: filtervisitor.String, Selectable: true},
		descriptionTableField: {Name: descriptionTableField, QueryLiteral: safesql.New("c.Description"), Type: filtervisitor.String, Selectable: true, Nullable: true},
		xidTableField:         {Name: xidTableField, QueryLiteral: safesql.New("c.Xid"), Type: filtervisitor.Integer, Selectable: true},
		widTableField:         {Name: widTableField, QueryLiteral: safesql.New("c.Wid"), Type: filtervisitor.Integer, Selectable: true},
		plotLayoutTableField:  {Name: plotLayoutTableField, QueryLiteral: safesql.New("c.PlotLayout"), Type: filtervisitor.Bytes, Selectable: true},
		plotSpecsTableField:   {Name: plotSpecsTableField, QueryLiteral: chartPlotSpecsSQL, Selectable: true, Derived: true},
		ownerTableField:       {Name: ownerTableField, QueryLiteral: safesql.New("c.Owner"), Type: filtervisitor.String, Selectable: true},
		createdTableField:     {Name: createdTableField, QueryLiteral: safesql.New("c.CreatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
		updatedTableField:     {Name: updatedTableField, QueryLiteral: safesql.New("c.UpdatedAt"), Type: filtervisitor.Timestamp, Selectable: true},
	}

	chartsTableView = querytools.TableView{
		Name:       querytools.TableName(chartsTableName),
		NameClause: safesql.New("Charts c"),
		Cost:       10,
		Fields:     chartFields,
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: chartIDTableField, SortOrder: querytools.Asc},
		},
	}

	// dashboardChartsJoinedTableView serves ListCharts for a dashboard parent, which filters on
	// dashboard_id: a field only the DashboardCharts link table has.
	dashboardChartsJoinedTableView = querytools.TableView{
		Name:       querytools.TableName("DashboardChartsWithCharts"),
		NameClause: safesql.New("DashboardCharts dc JOIN Charts c ON (dc.ChartId = c.ChartId)"),
		Cost:       20,
		Fields:     withDashboardLinkFields(chartFields),
		OrderedIndicesWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: dashboardIDTableField, SortOrder: querytools.Asc},
			{TableField: chartIDTableField, SortOrder: querytools.Asc},
		},
		OrderByFieldsWithSortDirection: []querytools.UserOrderedIndex{
			{TableField: chartIDTableField, SortOrder: querytools.Asc},
		},
	}

	// dashboardTables is the list of TableViews for Dashboard queries.
	dashboardTables = []querytools.TableView{dashboardsTableView}

	// chartTables is the list of TableViews for Chart queries.
	chartTables = []querytools.TableView{chartsTableView, dashboardChartsJoinedTableView}

	// dashboardsSelectFields is the list of fields selected for Dashboard queries, in the order
	// scanDashboardRow reads them.
	dashboardsSelectFields = []querytools.TableField{
		dashboardIDTableField,
		titleTableField,
		descriptionTableField,
		ownerTableField,
		chartLayoutTableField,
		chartIDsTableField,
		createdTableField,
		updatedTableField,
	}

	// dashboardsOrderByFields is the list of fields that can be used for ordering Dashboard queries.
	dashboardsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: dashboardIDTableField},
		{TableField: titleTableField},
		{TableField: ownerTableField},
	}

	// chartsSelectFields is the list of fields selected for Chart queries, in the order scanChartRow
	// reads them.
	chartsSelectFields = []querytools.TableField{
		chartIDTableField,
		titleTableField,
		descriptionTableField,
		xidTableField,
		widTableField,
		plotLayoutTableField,
		ownerTableField,
		plotSpecsTableField,
		createdTableField,
		updatedTableField,
	}

	// chartsOrderByFields is the list of fields valid for ordering Chart queries.
	chartsOrderByFields = []querytools.UserOrderedIndex{
		{TableField: createdTableField},
		{TableField: updatedTableField},
		{TableField: chartIDTableField},
		{TableField: titleTableField},
		{TableField: xidTableField},
		{TableField: widTableField},
		{TableField: ownerTableField},
	}
)

// withDashboardLinkFields returns fields extended with the DashboardCharts link columns.
func withDashboardLinkFields(fields map[querytools.TableField]querytools.Member) map[querytools.TableField]querytools.Member {
	joined := make(map[querytools.TableField]querytools.Member, len(fields)+2)
	for name, member := range fields {
		joined[name] = member
	}
	joined[dashboardIDTableField] = querytools.Member{Name: dashboardIDTableField, QueryLiteral: safesql.New("dc.DashboardId"), Type: filtervisitor.String, Selectable: true, Indexed: true}
	joined[dashboardChartIDTableField] = querytools.Member{Name: dashboardChartIDTableField, QueryLiteral: safesql.New("dc.DashboardChartId"), Type: filtervisitor.String, Selectable: true, Indexed: true}
	return joined
}

// newDashboardsQueryGen returns a query generator over the Dashboards view.
//
// Generators are built per query rather than shared by the Repository: GenerateQuery records the
// table it selected from on the generator, so a shared instance would race between concurrent
// list calls. Building one is a handful of allocations.
func newDashboardsQueryGen() (querytools.QueryGenerator, error) {
	qg, err := querytools.NewQueryGenerator(dashboardTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboards query generator: %w", err)
	}
	return qg, nil
}

// newChartsQueryGen returns a query generator over the Charts views.
func newChartsQueryGen() (querytools.QueryGenerator, error) {
	qg, err := querytools.NewQueryGenerator(chartTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create charts query generator: %w", err)
	}
	return qg, nil
}
