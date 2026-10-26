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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	dpb "xmanager_cloud/api/protos/dashboard"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

const (
	// lockWaitTimeout bounds how long a test waits for a concurrent writer to block on a row lock.
	lockWaitTimeout = 10 * time.Second
	// lockPollInterval is how often a test polls pg_stat_activity for a blocked writer.
	lockPollInterval = 10 * time.Millisecond
)

// setupDashboardRepository returns a Repository backed by a fresh ESS test database, together with
// the database's pool for raw SQL assertions.
func setupDashboardRepository(ctx context.Context, t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(ctx, t)
	return NewRepository(essPool), essPool
}

func blankDashboardProto(name string) *dpb.Dashboard {
	return &dpb.Dashboard{
		Name:        name,
		Title:       "Test Dashboard Title",
		Description: "Test Dashboard Description",
		Owner:       "user@example.com",
		ChartIds:    []string{},
		ChartLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 2,
		},
	}
}

func blankChartProto(chartID string, xid, wid int64) *dpb.Chart {
	return &dpb.Chart{
		Name:        validator.ConstructWorkUnitChartName(xid, wid, chartID),
		Title:       "Test Chart Title",
		Description: "Test Chart Description",
		Xid:         xid,
		Wid:         wid,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "plot-1",
				Title:  "Loss vs Epochs",
				Type:   dpb.PlotType_SCATTER,
			},
		},
		PlotLayout: &dpb.LayoutSpec{
			Type:    dpb.LayoutSpec_GRID,
			Columns: 1,
		},
	}
}

// seedChart creates a chart with the shared fixture contents and returns it.
func seedChart(ctx context.Context, t *testing.T, repo *Repository, chartID string) *dpb.Chart {
	t.Helper()
	chart := blankChartProto(chartID, 100, 1)
	if err := repo.CreateChart(ctx, chart); err != nil {
		t.Fatalf("CreateChart(%s) failed: %v", chartID, err)
	}
	return chart
}

// seedDashboard creates a dashboard with the shared fixture contents, linked to chartIDs, and
// returns it.
func seedDashboard(ctx context.Context, t *testing.T, repo *Repository, dashboardID string, chartIDs ...string) *dpb.Dashboard {
	t.Helper()
	dash := blankDashboardProto(validator.ConstructDashboardName(dashboardID))
	dash.ChartIds = chartIDs
	if err := repo.CreateDashboard(ctx, dash, nil); err != nil {
		t.Fatalf("CreateDashboard(%s) failed: %v", dashboardID, err)
	}
	return dash
}

// seedArtifact inserts, with raw SQL, the Experiments and Artifacts rows an ArtifactCharts row needs
// as its parent.
func seedArtifact(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid, artifactID int64) {
	t.Helper()
	const insertExperiment = `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
VALUES ($1, 'Test', 'test-user', 1, '{}', '{}', 'Notes', '{}', '', 0, 0, now(), now())
ON CONFLICT (Xid) DO NOTHING`
	if _, err := pool.Exec(ctx, insertExperiment, xid); err != nil {
		t.Fatalf("failed to seed experiment %d: %v", xid, err)
	}
	const insertArtifact = `INSERT INTO Artifacts (Xid, WorkUnitId, ArtifactId, Url, Icon, DisplayName, Priority, Tags, Notes, LifecyclePhase, DataType, MimeType, CreateTime, UpdateTime)
VALUES ($1, $2, $3, 'http://test', 'icon', 'Test Artifact', 1, '{}', 'Notes', 0, 0, 'text/plain', now(), now())`
	if _, err := pool.Exec(ctx, insertArtifact, xid, wid, artifactID); err != nil {
		t.Fatalf("failed to seed artifact %d of experiment %d: %v", artifactID, xid, err)
	}
}

// seedArtifactChart inserts, with raw SQL, an ArtifactCharts row referencing chartID.
func seedArtifactChart(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, wid, artifactID int64, artifactChartID, chartID string) {
	t.Helper()
	const insertArtifactChart = `INSERT INTO ArtifactCharts (Xid, WorkUnitId, ArtifactId, ArtifactChartId, ChartId, CreatedAt)
VALUES ($1, $2, $3, $4, $5, now())`
	if _, err := pool.Exec(ctx, insertArtifactChart, xid, wid, artifactID, artifactChartID, chartID); err != nil {
		t.Fatalf("failed to seed artifact chart %s for chart %s: %v", artifactChartID, chartID, err)
	}
}

// countRows returns, with raw SQL that bypasses the repository, the number of rows query matches.
func countRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("QueryRow(%q) failed: %v", query, err)
	}
	return count
}

// beginLockingTx begins a transaction that holds a FOR NO KEY UPDATE lock on one row until the
// test commits or rolls it back. The transaction is rolled back when the test completes if the
// test has not ended it itself.
func beginLockingTx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, lockSQL string, args ...any) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() failed: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, lockSQL, args...); err != nil {
		t.Fatalf("Exec(%q) failed: %v", lockSQL, err)
	}
	return tx
}

// waitForLockWaiter blocks until some backend of the test database waits on a lock, which is how a
// concurrent repository writer looks while a test transaction holds the row it needs.
func waitForLockWaiter(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const query = `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		if waiting := countRows(ctx, t, pool, query); waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no backend waited on a row lock within %v", lockWaitTimeout)
		}
		time.Sleep(lockPollInterval)
	}
}

// assertErrorIs fails the test unless err wraps want and carries its gRPC status code.
func assertErrorIs(t *testing.T, op string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s error = %v, want %v", op, err, want)
	}
	if got, wantCode := status.Code(err), status.Code(want); got != wantCode {
		t.Errorf("%s status code = %v, want %v (err: %v)", op, got, wantCode, err)
	}
}

// assertCode fails the test unless err carries the gRPC status code want.
func assertCode(t *testing.T, op string, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Errorf("%s status code = %v, want %v (err: %v)", op, got, want, err)
	}
}
