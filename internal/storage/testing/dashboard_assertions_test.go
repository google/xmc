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
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// strictChartStore validates plots like the request boundary does, keeps charts by ID, and rejects a
// duplicate ID like the real backends do.
type strictChartStore struct {
	*storage.MockStorage
	charts  map[string]*dpb.Chart
	creates []string
}

func (s *strictChartStore) CreateChart(_ context.Context, chart *dpb.Chart) error {
	s.creates = append(s.creates, chart.GetName())
	_, _, id, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return grpcerr.ErrInvalidArgument
	}
	if err := validator.ValidateChartPlots(chart.GetPlots(), chart.GetWid() == 0); err != nil {
		return err
	}
	if _, ok := s.charts[id]; ok {
		return fmt.Errorf("chart %q: %w", id, grpcerr.ErrAlreadyExists)
	}
	s.charts[id] = proto.Clone(chart).(*dpb.Chart)
	return nil
}

func (s *strictChartStore) GetChart(_ context.Context, id string) (*dpb.Chart, error) {
	c, ok := s.charts[id]
	if !ok {
		return nil, grpcerr.ErrNotFound
	}
	return proto.Clone(c).(*dpb.Chart), nil
}

func TestAssertDuplicateChartRejected(t *testing.T) {
	workUnitChart := &dpb.Chart{
		Name: validator.ConstructWorkUnitChartName(100, 1, "dup"), Title: "Original", Xid: 100, Wid: 1,
		Plots: []*dpb.PlotSpec{{PlotId: "loss", Title: "Loss", Type: dpb.PlotType_SCATTER}},
	}
	tests := []struct {
		name     string
		base     *dpb.Chart
		xid, wid int64
		wantDup  string
	}{
		{name: "work-unit duplicate", base: workUnitChart, xid: 200, wid: 2, wantDup: "experiments/200/workUnits/2/charts/dup"},
		{name: "experiment-level duplicate of work-unit chart", base: workUnitChart, xid: 100, wantDup: "experiments/100/charts/dup"},
		{name: "experiment-level duplicate", base: ExperimentChartProto("dup", 100), xid: 100, wantDup: "experiments/100/charts/dup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &strictChartStore{MockStorage: &storage.MockStorage{}, charts: map[string]*dpb.Chart{}}
			AssertDuplicateChartRejected(t.Context(), t, store, tt.base, tt.xid, tt.wid)
			if len(store.creates) != 2 || store.creates[1] != tt.wantDup {
				t.Errorf("CreateChart names = %q, want [%q %q]", store.creates, tt.base.GetName(), tt.wantDup)
			}
		})
	}
}
