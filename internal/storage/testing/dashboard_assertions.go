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

package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// AssertDuplicateChartRejected creates base, then attempts a CreateChart that reuses base's chart
// ID under work unit (xid, wid), or under experiment xid when wid is 0, with different content. It
// fails t unless the duplicate returns grpcerr.ErrAlreadyExists with codes.AlreadyExists and the
// stored chart is left unchanged.
func AssertDuplicateChartRejected(ctx context.Context, t *testing.T, store storage.DashboardStore, base *dpb.Chart, xid, wid int64) {
	t.Helper()
	_, _, chartID, err := validator.ValidateChartName(base.GetName())
	if err != nil {
		t.Fatalf("ValidateChartName(%q) failed: %v", base.GetName(), err)
	}
	if err := store.CreateChart(ctx, base); err != nil {
		t.Fatalf("CreateChart(original) failed: %v", err)
	}
	want, err := store.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(original) failed: %v", err)
	}
	dup := duplicateChart(base, chartID, xid, wid)
	if err := store.CreateChart(ctx, dup); !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CreateChart(duplicate) error = %v (code %v), want %v", err, status.Code(err), grpcerr.ErrAlreadyExists)
	}
	got, err := store.GetChart(ctx, chartID)
	if err != nil {
		t.Fatalf("GetChart(after duplicate) failed: %v", err)
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("chart changed after rejected duplicate CreateChart (-want +got):\n%s", diff)
	}
}

// duplicateChart returns a valid chart that reuses chartID under work unit (xid, wid), or under
// experiment xid when wid is 0. It retitles base's first plot and adds one more, using plot types
// the target chart level allows, so a non-strict insert would visibly change the stored chart.
func duplicateChart(base *dpb.Chart, chartID string, xid, wid int64) *dpb.Chart {
	name, replacedType, extraType := validator.ConstructWorkUnitChartName(xid, wid, chartID), dpb.PlotType_BAR, dpb.PlotType_SCATTER
	if wid == 0 {
		name, replacedType, extraType = validator.ConstructExperimentChartName(xid, chartID), dpb.PlotType_PARALLEL_COORDINATES, dpb.PlotType_PARALLEL_COORDINATES
	}
	replacedID := "plot-1"
	if plots := base.GetPlots(); len(plots) > 0 {
		replacedID = plots[0].GetPlotId()
	}
	return prototest.Modify(base, func(c *dpb.Chart) {
		c.Name, c.Xid, c.Wid, c.Title = name, xid, wid, "Duplicate Title"
		c.Plots = []*dpb.PlotSpec{
			{PlotId: replacedID, Title: "Replaced", Type: replacedType},
			{PlotId: replacedID + "-extra", Title: "Extra", Type: extraType},
		}
	})
}
