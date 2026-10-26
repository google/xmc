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
	"context"
	"strings"
	"testing"
	"time"

	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	storagedata "xmanager_cloud/internal/storage/data"
)

func TestDashboardsQueryGen_DefaultAndFilteredSQL(t *testing.T) {
	ctx := context.Background()
	queryGen, err := newDashboardsQueryGen()
	if err != nil {
		t.Fatalf("newDashboardsQueryGen() failed: %v", err)
	}

	normalizedFilter, err := normalizer.Normalize(`owner = "alice@example.com"`, queryGen.Tables()[0])
	if err != nil {
		t.Fatalf("normalizer.Normalize() failed: %v", err)
	}

	stmt := postgresdialect.NewStatement("")
	err = ls.ListStatement(ctx, stmt, ls.ListParams{
		PageSize:            25,
		Filter:              normalizedFilter,
		QueryGen:            queryGen,
		DefaultSelectFields: dashboardsSelectFields,
		ValidOrderByFields:  dashboardsOrderByFields,
	})
	if err != nil {
		t.Fatalf("ListStatement() failed: %v", err)
	}

	sql := stmt.SQL
	for _, wantSubstr := range []string{
		"FROM Dashboards d",
		"ARRAY(SELECT dc.ChartId FROM DashboardCharts dc WHERE dc.DashboardId = d.DashboardId ORDER BY dc.ChartId)",
		"d.Owner = $",
		"ORDER BY d.CreatedAt DESC, d.DashboardId",
		"LIMIT 26",
	} {
		if !strings.Contains(sql, wantSubstr) {
			t.Errorf("generated SQL %q does not contain %q", sql, wantSubstr)
		}
	}
}

func TestChartsQueryGen_SelectsRootAndJoinedViews(t *testing.T) {
	ctx := context.Background()

	t.Run("no parent uses Charts c view", func(t *testing.T) {
		queryGen, err := newChartsQueryGen()
		if err != nil {
			t.Fatalf("newChartsQueryGen() failed: %v", err)
		}
		normalizedFilter, err := normalizer.Normalize("xid = 100 AND wid = 1", queryGen.Tables()[0])
		if err != nil {
			t.Fatalf("normalizer.Normalize() failed: %v", err)
		}
		stmt := postgresdialect.NewStatement("")
		err = ls.ListStatement(ctx, stmt, ls.ListParams{
			PageSize:            10,
			Filter:              normalizedFilter,
			QueryGen:            queryGen,
			DefaultSelectFields: chartsSelectFields,
			ValidOrderByFields:  chartsOrderByFields,
		})
		if err != nil {
			t.Fatalf("ListStatement() failed: %v", err)
		}
		if !strings.Contains(stmt.SQL, "FROM Charts c") {
			t.Errorf("generated SQL %q does not select from Charts c", stmt.SQL)
		}
		if strings.Contains(stmt.SQL, "JOIN") {
			t.Errorf("generated SQL %q unexpectedly joins DashboardCharts", stmt.SQL)
		}
	})

	t.Run("dashboard parent uses DashboardCharts JOIN Charts view", func(t *testing.T) {
		queryGen, err := newChartsQueryGen()
		if err != nil {
			t.Fatalf("newChartsQueryGen() failed: %v", err)
		}
		filter, err := storagedata.BuildListChartsFilter("dashboards/dash-1", "")
		if err != nil {
			t.Fatalf("BuildListChartsFilter() failed: %v", err)
		}
		normalizedFilter, err := normalizer.Normalize(filter, queryGen.Tables()[0])
		if err != nil {
			t.Fatalf("normalizer.Normalize() failed: %v", err)
		}
		stmt := postgresdialect.NewStatement("")
		err = ls.ListStatement(ctx, stmt, ls.ListParams{
			PageSize:            10,
			Filter:              normalizedFilter,
			QueryGen:            queryGen,
			DefaultSelectFields: chartsSelectFields,
			ValidOrderByFields:  chartsOrderByFields,
		})
		if err != nil {
			t.Fatalf("ListStatement() failed: %v", err)
		}
		if !strings.Contains(stmt.SQL, "FROM DashboardCharts dc JOIN Charts c ON (dc.ChartId = c.ChartId)") {
			t.Errorf("generated SQL %q does not join DashboardCharts and Charts", stmt.SQL)
		}
	})
}

func TestRowData_ErrorPathsAndPadding(t *testing.T) {
	t.Run("dashboardRow.toProto missing ID", func(t *testing.T) {
		r := &dashboardRow{}
		if _, err := r.toProto(); err == nil {
			t.Errorf("dashboardRow{}.toProto() succeeded, want error")
		}
	})

	t.Run("dashboardRow.toProto corrupt layout bytes", func(t *testing.T) {
		r := &dashboardRow{
			ID:          "d-1",
			ChartLayout: []byte{0xff, 0xff, 0xff},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if _, err := r.toProto(); err == nil {
			t.Errorf("dashboardRow.toProto() with invalid layout succeeded, want error")
		}
	})

	t.Run("chartRow.toProto missing ID", func(t *testing.T) {
		r := &chartRow{}
		if _, err := r.toProto(); err == nil {
			t.Errorf("chartRow{}.toProto() succeeded, want error")
		}
	})

	t.Run("chartRow.toProto corrupt layout bytes", func(t *testing.T) {
		r := &chartRow{
			ID:         "c-1",
			PlotLayout: []byte{0xff, 0xff, 0xff},
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if _, err := r.toProto(); err == nil {
			t.Errorf("chartRow.toProto() with invalid layout succeeded, want error")
		}
	})

	t.Run("chartRow.toProto corrupt plot bytes", func(t *testing.T) {
		r := &chartRow{
			ID:        "c-1",
			PlotSpecs: [][]byte{{0xff, 0xff, 0xff}},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if _, err := r.toProto(); err == nil {
			t.Errorf("chartRow.toProto() with invalid plot bytes succeeded, want error")
		}
	})

	t.Run("padScanDest appends dummy destinations for extra columns", func(t *testing.T) {
		base := []any{new(string), new(int)}
		if got := padScanDest(base, 2); len(got) != 2 {
			t.Errorf("padScanDest(len=2, numColumns=2) len = %d, want 2", len(got))
		}
		if got := padScanDest(base, 4); len(got) != 4 {
			t.Errorf("padScanDest(len=2, numColumns=4) len = %d, want 4", len(got))
		}
	})
}
