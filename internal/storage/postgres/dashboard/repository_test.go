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
	"strings"
	"testing"

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

func TestNewRepository_NilPoolFailsPreconditionOnTransactionalWrites(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)
	if repo.db != nil || repo.pool != nil {
		t.Fatalf("NewRepository(nil) = {db:%v, pool:%v}, want both nil", repo.db, repo.pool)
	}

	dash := blankDashboardProto(validator.ConstructDashboardName("d-nil-pool"))
	assertErrorIs(t, "CreateDashboard(nil pool)", repo.CreateDashboard(ctx, dash, nil), grpcerr.ErrFailedPrecondition)

	_, err := repo.UpdateDashboard(ctx, dash, nil)
	assertErrorIs(t, "UpdateDashboard(nil pool)", err, grpcerr.ErrFailedPrecondition)

	chart := blankChartProto("c-nil-pool", 100, 1)
	assertErrorIs(t, "CreateChart(nil pool)", repo.CreateChart(ctx, chart), grpcerr.ErrFailedPrecondition)

	_, err = repo.UpdateChart(ctx, chart, nil)
	assertErrorIs(t, "UpdateChart(nil pool)", err, grpcerr.ErrFailedPrecondition)

	assertErrorIs(t, "DeleteChart(nil pool)", repo.DeleteChart(ctx, "c-nil-pool"), grpcerr.ErrFailedPrecondition)
}

func TestLockingSQL_UsesForNoKeyUpdate(t *testing.T) {
	for name, sql := range map[string]string{
		"lockDashboardSQL": lockDashboardSQL.String(),
		"lockChartSQL":     lockChartSQL.String(),
		"lockChartKeySQL":  lockChartKeySQL.String(),
	} {
		if !strings.Contains(sql, "FOR NO KEY UPDATE") {
			t.Errorf("%s = %q, want FOR NO KEY UPDATE", name, sql)
		}
		withoutNoKey := strings.ReplaceAll(sql, "FOR NO KEY UPDATE", "")
		if strings.Contains(withoutNoKey, "FOR UPDATE") {
			t.Errorf("%s = %q contains bare FOR UPDATE", name, sql)
		}
	}
}
