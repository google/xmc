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
	"errors"
	"testing"

	"cloud.google.com/go/spanner"

	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage/spanner/schema/dashboardschema"
)

func TestReadRowInTransaction(t *testing.T) {
	ctx := context.Background()
	repo := setupDashboardRepository(ctx, t)

	seedChart(ctx, t, repo, "test-read-row-in-transaction")

	tests := []struct {
		name         string
		chartID      string
		wantNotFound bool
	}{
		{name: "existing row", chartID: "test-read-row-in-transaction"},
		{name: "missing row", chartID: "no-such-chart", wantNotFound: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
				row, err := readRowInTransaction(ctx, txn, dashboardschema.ChartsTableName, "chart", tc.chartID, dashboardschema.FetchedChartColumns)
				if tc.wantNotFound {
					if !errors.Is(err, grpcerr.ErrNotFound) {
						t.Errorf("readRowInTransaction error = %v, want grpcerr.ErrNotFound", err)
					}
					return nil
				}
				if err != nil {
					t.Errorf("readRowInTransaction failed: %v", err)
					return nil
				}
				if row == nil {
					t.Error("readRowInTransaction returned a nil row")
				}
				return nil
			})
			if err != nil {
				t.Fatalf("ReadWriteTransaction failed: %v", err)
			}
		})
	}
}
