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

package clickhouse

import (
	"strings"
	"testing"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestTranslatePlotQuery_SummaryBox(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "plot-1",
		Type:   dashboardpb.PlotType_SUMMARY_BOX,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"loss/train"},
		},
	}

	q, err := TranslatePlotQuery(plot, 1001, 5, nil, "xmc_metrics")
	if err != nil {
		t.Fatalf("TranslatePlotQuery failed: %v", err)
	}

	if !strings.Contains(q.SQL, "argMax(metric_value, step) AS latest_value") {
		t.Errorf("expected argMax latest_value in SQL, got: %s", q.SQL)
	}
	if len(q.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(q.Args))
	}
}

func TestTranslatePlotQuery_ScatterStandard(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "plot-2",
		Type:   dashboardpb.PlotType_SCATTER,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"accuracy/val"},
		},
	}
	req := &dashboardpb.GetChartDataRequest{
		StartIteration: 10,
		EndIteration:   100,
	}

	q, err := TranslatePlotQuery(plot, 1001, 5, req, "xmc_metrics")
	if err != nil {
		t.Fatalf("TranslatePlotQuery failed: %v", err)
	}

	if !strings.Contains(q.SQL, "ORDER BY step ASC") {
		t.Errorf("expected ORDER BY step ASC in SQL, got: %s", q.SQL)
	}
	if len(q.Args) != 5 {
		t.Errorf("expected 5 args (xid, wid, metrics, start, end), got %d", len(q.Args))
	}
}

func TestTranslatePlotQuery_ScatterLTTB(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "plot-3",
		Type:   dashboardpb.PlotType_SCATTER,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"accuracy/val"},
		},
	}
	req := &dashboardpb.GetChartDataRequest{
		ChartWidthPixels: 500,
	}

	q, err := TranslatePlotQuery(plot, 1001, 5, req, "xmc_metrics")
	if err != nil {
		t.Fatalf("TranslatePlotQuery failed: %v", err)
	}

	if !strings.Contains(q.SQL, "lttb(?)(step, metric_value) AS points") {
		t.Errorf("expected native ClickHouse lttb function in SQL, got: %s", q.SQL)
	}
	if q.Args[0] != int32(500) {
		t.Errorf("expected first arg to be chart_width_pixels (500), got %v", q.Args[0])
	}
}

func TestTranslatePlotQuery_Bar(t *testing.T) {
	plot := &dashboardpb.PlotSpec{
		PlotId: "plot-4",
		Type:   dashboardpb.PlotType_BAR,
		Data: &dashboardpb.DataSpec{
			Metrics: []string{"status_code"},
		},
	}

	q, err := TranslatePlotQuery(plot, 1001, 5, nil, "xmc_metrics")
	if err != nil {
		t.Fatalf("TranslatePlotQuery failed: %v", err)
	}

	if !strings.Contains(q.SQL, "toString(metric_value) AS category_or_bin") {
		t.Errorf("expected category_or_bin in SQL, got: %s", q.SQL)
	}
}
