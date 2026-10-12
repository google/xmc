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

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
)

// CreateDashboard writes a new dashboard, the charts created with it and their child
// dashboard-chart relationships to Spanner in a single commit.
func (r *Repository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
	logging.Info(ctx, "Create dashboard in storage", "name", dashboard.GetName(), "charts", len(charts))

	mutations, err := data.DashboardWithChartsToInsertMutations(dashboard, charts)
	if err != nil {
		logging.Error(ctx, "Failed to generate create dashboard mutations", "error", err)
		return err
	}

	_, err = r.client.Apply(ctx, mutations)
	if err != nil {
		logging.Error(ctx, "Failed to apply create dashboard mutations", "error", err)
		return err
	}
	return nil
}
