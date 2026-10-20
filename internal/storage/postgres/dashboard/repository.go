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

// Package dashboard contains the concrete PostgreSQL repository implementation for dashboards and charts.
package dashboard

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/storage"
)

var _ storage.DashboardStore = (*Repository)(nil)

// Repository manages persistence for dashboard and chart entities in PostgreSQL.
type Repository struct{}

// NewRepository creates a new PostgreSQL Dashboard repository instance.
func NewRepository() *Repository {
	return &Repository{}
}

// CreateDashboard creates a new dashboard and its associated charts.
func (r *Repository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error {
	return status.Error(codes.Unimplemented, "postgres dashboard store: CreateDashboard is not implemented")
}

// GetDashboard retrieves a dashboard by its unique ID.
func (r *Repository) GetDashboard(ctx context.Context, dashboardID string) (*dpb.Dashboard, error) {
	return nil, status.Error(codes.Unimplemented, "postgres dashboard store: GetDashboard is not implemented")
}

// ListDashboards retrieves a paginated and filtered list of dashboards.
func (r *Repository) ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres dashboard store: ListDashboards is not implemented")
}

// UpdateDashboard updates a dashboard with the given update mask.
func (r *Repository) UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fmpb.FieldMask) (*dpb.Dashboard, error) {
	return nil, status.Error(codes.Unimplemented, "postgres dashboard store: UpdateDashboard is not implemented")
}

// DeleteDashboard deletes a dashboard by its unique ID.
func (r *Repository) DeleteDashboard(ctx context.Context, dashboardID string) error {
	return status.Error(codes.Unimplemented, "postgres dashboard store: DeleteDashboard is not implemented")
}

// CreateChart creates a new chart.
func (r *Repository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	return status.Error(codes.Unimplemented, "postgres dashboard store: CreateChart is not implemented")
}

// GetChart retrieves a chart by its unique ID.
func (r *Repository) GetChart(ctx context.Context, chartID string) (*dpb.Chart, error) {
	return nil, status.Error(codes.Unimplemented, "postgres dashboard store: GetChart is not implemented")
}

// ListCharts retrieves a paginated and filtered list of charts.
func (r *Repository) ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error) {
	return nil, "", status.Error(codes.Unimplemented, "postgres dashboard store: ListCharts is not implemented")
}

// UpdateChart updates a chart with the given update mask.
func (r *Repository) UpdateChart(ctx context.Context, chart *dpb.Chart, updateMask *fmpb.FieldMask) (*dpb.Chart, error) {
	return nil, status.Error(codes.Unimplemented, "postgres dashboard store: UpdateChart is not implemented")
}

// DeleteChart deletes a chart by its unique ID.
func (r *Repository) DeleteChart(ctx context.Context, chartID string) error {
	return status.Error(codes.Unimplemented, "postgres dashboard store: DeleteChart is not implemented")
}
