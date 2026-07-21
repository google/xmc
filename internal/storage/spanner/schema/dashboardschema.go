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

// Package dashboardschema contains the schema constants for Dashboard Spanner tables.
package dashboardschema

const (
	DashboardsTableName      = "Dashboards"
	DashboardChartsTableName = "DashboardCharts"
	ChartsTableName          = "Charts"
	ChartPlotsTableName      = "ChartPlots"
	ArtifactChartsTableName  = "ArtifactCharts"
)

var FetchedDashboardColumns = []string{
	"DashboardId",
	"Title",
	"Description",
	"Owner",
	"ChartLayout",
	"CreatedAt",
	"UpdatedAt",
}

var FetchedChartColumns = []string{
	"ChartId",
	"Title",
	"Xid",
	"PlotLayout",
	"Owner",
	"CreatedAt",
	"UpdatedAt",
}
