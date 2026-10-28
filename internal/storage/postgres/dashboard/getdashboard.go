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

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
)

// GetDashboard retrieves a dashboard by its unique ID along with its layout and child chart IDs.
func (r *Repository) GetDashboard(ctx context.Context, dashboardID string) (*dpb.Dashboard, error) {
	if dashboardID == "" {
		return nil, fmt.Errorf("dashboardId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	logging.Info(ctx, "Get dashboard from storage", "dashboardId", dashboardID)

	return fetchDashboard(ctx, r.db, selectDashboardSQL, dashboardID)
}
