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

package dashboardschema

import (
	"testing"

	"xmanager_cloud/internal/query_generator/querytools"
)

func TestDashboardAndChartTablesCompileQueryGenerators(t *testing.T) {
	dashGen, err := querytools.NewQueryGenerator(DashboardTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		t.Fatalf("NewQueryGenerator(DashboardTables) failed: %v", err)
	}
	if len(dashGen.Tables()) != 1 {
		t.Errorf("len(dashGen.Tables()) = %d, want 1", len(dashGen.Tables()))
	}

	chartGen, err := querytools.NewQueryGenerator(ChartTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		t.Fatalf("NewQueryGenerator(ChartTables) failed: %v", err)
	}
	if len(chartGen.Tables()) != 2 {
		t.Errorf("len(chartGen.Tables()) = %d, want 2", len(chartGen.Tables()))
	}
}
