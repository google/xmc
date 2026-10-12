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
	"testing"

	"cloud.google.com/go/spanner"
	dpb "github.com/google/xmc/api/protos/dashboard"
	"github.com/google/xmc/internal/storage/spanner/emulator"
	"github.com/google/xmc/internal/validator"
)

func setupDashboardRepository(ctx context.Context, t *testing.T) *Repository {
	t.Helper()
	return setupDashboardRepositoryWithParent(ctx, t, 100, 1)
}

func setupDashboardRepositoryWithParent(ctx context.Context, t *testing.T, xid, wid int64) *Repository {
	t.Helper()
	clients := emulator.SetupWithEmulator(ctx, t)
	essClient := clients.ESSClient

	seedParent(ctx, t, essClient, xid, wid)

	repo, err := NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository failed: %v", err)
	}
	return repo
}

func seedParent(ctx context.Context, t *testing.T, client *spanner.Client, xid, wid int64) {
	t.Helper()
	mExp := spanner.InsertOrUpdate("Experiments", []string{
		"Xid", "Title", "Creator", "Priority", "Urls", "Tags", "Notes", "Resources", "Status", "CreateTime", "UpdateTime",
	}, []interface{}{
		xid, "Test", "test-user", int64(1), []string{}, []string{}, "Notes", [][]byte{}, []byte{}, spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	mArt := spanner.InsertOrUpdate("Artifacts", []string{
		"Xid", "WorkUnitId", "ArtifactId", "Url", "Icon", "DisplayName", "Priority", "Tags", "Notes", "LifecyclePhase", "DataType", "MimeType", "CreateTime", "UpdateTime",
	}, []interface{}{
		xid, wid, int64(0), "http://test", "icon", "Test Artifact", int64(1), []string{}, "Notes", int64(0), int64(0), "text/plain", spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	if _, err := client.Apply(ctx, []*spanner.Mutation{mExp, mArt}); err != nil {
		t.Fatalf("failed to seed parent test data for xid %d: %v", xid, err)
	}
}

func blankDashboardProto(name string) *dpb.Dashboard {
	return &dpb.Dashboard{
		Name:        name,
		Title:       "Test Dashboard Title",
		Description: "Test Dashboard Description",
		Owner:       "user@example.com",
		ChartIds:    []string{},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
		},
	}
}

func blankChartProto(chartID string, xid, wid int64) *dpb.Chart {
	return &dpb.Chart{
		Name:        validator.ConstructWorkUnitChartName(xid, wid, chartID),
		Title:       "Test Chart Title",
		Description: "Test Chart Description",
		Xid:         xid,
		Wid:         wid,
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
