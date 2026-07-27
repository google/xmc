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
	"context"
	"testing"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestFilterPlots(t *testing.T) {
	plots := []*dashboardpb.PlotSpec{
		{PlotId: "p1"},
		{PlotId: "p2"},
		{PlotId: "p3"},
	}

	filtered := filterPlots(plots, []string{"p1", "p3"})
	if len(filtered) != 2 {
		t.Fatalf("got %d plots, want 2", len(filtered))
	}
	if filtered[0].PlotId != "p1" || filtered[1].PlotId != "p3" {
		t.Errorf("got plot IDs [%s, %s], want [p1, p3]", filtered[0].PlotId, filtered[1].PlotId)
	}

	allPlots := filterPlots(plots, nil)
	if len(allPlots) != 3 {
		t.Errorf("got %d plots, want 3 when allowedIDs is nil", len(allPlots))
	}
}

func TestGetChartData_NilValidation(t *testing.T) {
	client := NewClient(nil)
	ctx := context.Background()

	if _, err := client.GetChartData(ctx, nil, &dashboardpb.Chart{}); err == nil {
		t.Error("expected error for nil request, got nil")
	}

	if _, err := client.GetChartData(ctx, &dashboardpb.GetChartDataRequest{}, nil); err == nil {
		t.Error("expected error for nil chart, got nil")
	}
}
