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

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository(nil)
	if repo == nil {
		t.Fatal("NewRepository() returned nil")
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "CreateDashboard",
			call: func() error {
				return repo.CreateDashboard(ctx, nil, nil)
			},
		},
		{
			name: "GetDashboard",
			call: func() error {
				_, err := repo.GetDashboard(ctx, "dash-1")
				return err
			},
		},
		{
			name: "ListDashboards",
			call: func() error {
				_, _, err := repo.ListDashboards(ctx, 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "UpdateDashboard",
			call: func() error {
				_, err := repo.UpdateDashboard(ctx, nil, nil)
				return err
			},
		},
		{
			name: "DeleteDashboard",
			call: func() error {
				return repo.DeleteDashboard(ctx, "dash-1")
			},
		},
		{
			name: "CreateChart",
			call: func() error {
				return repo.CreateChart(ctx, nil)
			},
		},
		{
			name: "GetChart",
			call: func() error {
				_, err := repo.GetChart(ctx, "chart-1")
				return err
			},
		},
		{
			name: "ListCharts",
			call: func() error {
				_, _, err := repo.ListCharts(ctx, "dashboards/dash-1", 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "UpdateChart",
			call: func() error {
				_, err := repo.UpdateChart(ctx, nil, nil)
				return err
			},
		},
		{
			name: "DeleteChart",
			call: func() error {
				return repo.DeleteChart(ctx, "chart-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, codes.Unimplemented, err)
			}
		})
	}
}

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}
}
