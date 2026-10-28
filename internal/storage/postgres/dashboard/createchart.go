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

	"github.com/jackc/pgx/v5"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

// CreateChart writes a new chart and its plot specifications to PostgreSQL in a single
// transaction. A chart whose ID is already taken fails with an AlreadyExists error.
func (r *Repository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	logging.Info(ctx, "Create chart in storage", "name", chart.GetName())

	insert, err := newChartInsert(chart)
	if err != nil {
		logging.Error(ctx, "Failed to prepare create chart", "error", err)
		return err
	}

	err = pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return insertChart(ctx, tx, insert)
	})
	if err != nil {
		logging.Error(ctx, "Failed to create chart", "error", err)
		return err
	}
	return nil
}
