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
	"testing"

	dashboardpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestMapPlotRowsValidation(t *testing.T) {
	t.Run("nil plot", func(t *testing.T) {
		_, err := MapPlotRows(nil, nil)
		if err == nil {
			t.Errorf("expected error for nil plot")
		}
	})

	t.Run("unsupported plot type", func(t *testing.T) {
		plot := &dashboardpb.PlotSpec{
			PlotId: "p1",
			Type:   dashboardpb.PlotType_PLOT_TYPE_UNSPECIFIED,
		}
		_, err := MapPlotRows(plot, nil)
		if err == nil {
			t.Errorf("expected error for unspecified plot type")
		}
	})
}
