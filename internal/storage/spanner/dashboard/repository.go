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

// Package dashboard contains the concrete Spanner repository implementation for dashboards and charts.
package dashboard

import (
	"fmt"

	"cloud.google.com/go/spanner"

	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
)

// Repository manages persistence for dashboard and chart entities in Cloud Spanner.
type Repository struct {
	client                  *spanner.Client
	dashboardsQueryGen      querytools.QueryGenerator
	chartsQueryGen          querytools.QueryGenerator
	dashboardChartsQueryGen querytools.QueryGenerator
	chartPlotsQueryGen      querytools.QueryGenerator
	artifactChartsQueryGen  querytools.QueryGenerator
}

// NewRepository creates a new Dashboard repository instance with initialized query generators.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	dashboardsQueryGen, err := querytools.NewQueryGenerator(dashboardschema.DashboardTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboards query generator: %w", err)
	}

	chartsQueryGen, err := querytools.NewQueryGenerator(dashboardschema.ChartTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create charts query generator: %w", err)
	}

	dashboardChartsQueryGen, err := querytools.NewQueryGenerator(dashboardschema.DashboardChartsTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboard charts query generator: %w", err)
	}

	// Plots are read for a whole page of charts at once, so this generator opts into disjunction
	// optimization: the per-chart chart_id equalities collapse into one IN UNNEST clause instead of
	// one OR term per chart. Order-by optimization stays off to keep the existing sort semantics.
	chartPlotsQueryGen, err := querytools.NewOptimizableGenerator(dashboardschema.ChartPlotsTables, nil, nil, nil, true, false, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create chart plots query generator: %w", err)
	}

	artifactChartsQueryGen, err := querytools.NewQueryGenerator(dashboardschema.ArtifactChartsTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create artifact charts query generator: %w", err)
	}

	return &Repository{
		client:                  client,
		dashboardsQueryGen:      dashboardsQueryGen,
		chartsQueryGen:          chartsQueryGen,
		dashboardChartsQueryGen: dashboardChartsQueryGen,
		chartPlotsQueryGen:      chartPlotsQueryGen,
		artifactChartsQueryGen:  artifactChartsQueryGen,
	}, nil
}
