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
	"fmt"
	"testing"

	"cloud.google.com/go/spanner"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func setupDashboardRepository(ctx context.Context, t *testing.T) *Repository {
	t.Helper()
	_, _, emu := emulator.SetupWithDashboard(ctx, t)
	dbURI := fmt.Sprintf("projects/test-project/instances/test-instance/databases/%s", emu.DashboardDatabase)
	client, err := spanner.NewClient(ctx, dbURI, emu.ClientOptions()...)
	if err != nil {
		t.Fatalf("failed to create dashboard spanner client: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	repo, err := NewRepository(client)
	if err != nil {
		t.Fatalf("NewRepository failed: %v", err)
	}
	return repo
}

func blankDashboardProto(name string) *dpb.Dashboard {
	return &dpb.Dashboard{
		Name:        name,
		Title:       "Test Dashboard Title",
		Description: "Test Dashboard Description",
		Owner:       "user@example.com",
		Charts:      []*dpb.Chart{},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
		},
	}
}

func blankChartProto(name string, xid, wid int64) *dpb.Chart {
	return &dpb.Chart{
		Name:  name,
		Title: "Test Chart Title",
		Xid:   xid,
		Wid:   wid,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "plot-1",
				Title:  "Loss vs Epochs",
				Type:   dpb.PlotType_SCATTER,
			},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 1,
		},
	}
}
