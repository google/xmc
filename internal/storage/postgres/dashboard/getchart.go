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

// GetChart retrieves a chart by its unique ID along with its plot specifications and plot layout.
func (r *Repository) GetChart(ctx context.Context, chartID string) (*dpb.Chart, error) {
	if chartID == "" {
		return nil, fmt.Errorf("chartId must not be empty: %w", grpcerr.ErrInvalidArgument)
	}
	logging.Info(ctx, "Get chart from storage", "chartId", chartID)

	return fetchChart(ctx, r.db, selectChartSQL, chartID)
}
