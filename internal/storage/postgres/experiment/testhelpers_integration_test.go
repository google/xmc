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

package experiment

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

// lostUpdateWindow is how long each concurrent read-modify-write waits between its read and its
// write. Without a row lock, concurrent transactions then read the same experiment and overwrite
// each other's updates; with the lock, they merely run one after another.
const lostUpdateWindow = 20 * time.Millisecond

// derivedStateColumns holds the ActiveState and LaunchState columns, which are populated from
// Status on write and only readable with raw SQL.
type derivedStateColumns struct {
	ActiveState pb.ExperimentStatus_ActiveState
	LaunchState pb.ExperimentStatus_LaunchState
}

// newTestRepository returns a repository backed by a fresh ESS test database, together with the
// database's pool for raw SQL assertions.
func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return NewRepository(essPool), essPool
}

// runInTx runs fn in a read-write transaction of repo's pool with a TxRepository bound to it, the
// way postgres.Storage.RunInTransaction binds storage.Transaction.Experiments.
func runInTx(ctx context.Context, repo *Repository, fn func(ctx context.Context, tx *TxRepository) error) error {
	return pgdb.RunInTx(ctx, repo.pool, func(ctx context.Context, pgTx pgx.Tx) error {
		return fn(ctx, repo.WithTx(pgTx))
	})
}

// blankExperimentProto mirrors spanner.BlankExperimentProto: a minimal active experiment. ACLs
// are omitted because they live in the XID database, not in the Experiments table.
func blankExperimentProto(xid int64) *pb.Experiment {
	return &pb.Experiment{
		Name:      validator.ConstructExperimentName(xid),
		Title:     "test-title",
		Creator:   "test@example.com",
		Priority:  1,
		Urls:      []*pb.Url{},
		Tags:      []string{},
		Notes:     "",
		Resources: []*pb.ResourceSet{},
		Status: &pb.ExperimentStatus{
			ActiveState: pb.ExperimentStatus_ACTIVE,
		},
	}
}

// createExperiment stores exp the way internal/experiment.CreateExperiment does: it stamps the
// create, update and status update times with the current time unless exp already carries a
// CreateTime, inserts exp in a transaction of its own and returns it. Times are truncated to
// microseconds, the resolution of TIMESTAMPTZ, so that exp equals what reads return.
func createExperiment(ctx context.Context, t *testing.T, repo *Repository, exp *pb.Experiment) *pb.Experiment {
	t.Helper()
	if exp.Status == nil {
		exp.Status = &pb.ExperimentStatus{}
	}
	if exp.CreateTime == nil {
		exp.CreateTime = timestamppb.New(time.Now().Truncate(time.Microsecond))
	}
	if exp.UpdateTime == nil {
		exp.UpdateTime = exp.CreateTime
	}
	if exp.Status.UpdateTime == nil {
		exp.Status.UpdateTime = exp.CreateTime
	}
	if exp.Status.LastChildStatusUpdateTime == nil {
		exp.Status.LastChildStatusUpdateTime = exp.CreateTime
	}
	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.InsertExperiment(ctx, exp)
	})
	if err != nil {
		t.Fatalf("InsertExperiment(%q) unexpected error: %v", exp.GetName(), err)
	}
	return exp
}

// createBlankExperiment stores blankExperimentProto(xid) and returns it.
func createBlankExperiment(ctx context.Context, t *testing.T, repo *Repository, xid int64) *pb.Experiment {
	t.Helper()
	return createExperiment(ctx, t, repo, blankExperimentProto(xid))
}

// createExperimentsAt stores one blank experiment per xid in xids, in order, with create times
// base, base+1s, base+2s, ... so that creation order, XID order and create time order can be told
// apart by the caller's choice of xids.
func createExperimentsAt(ctx context.Context, t *testing.T, repo *Repository, base time.Time, xids ...int64) []*pb.Experiment {
	t.Helper()
	exps := make([]*pb.Experiment, 0, len(xids))
	for i, xid := range xids {
		exp := blankExperimentProto(xid)
		exp.CreateTime = timestamppb.New(base.Add(time.Duration(i) * time.Second))
		exps = append(exps, createExperiment(ctx, t, repo, exp))
	}
	return exps
}

// xidsOf returns the XIDs of exps, in order.
func xidsOf(t *testing.T, exps []*pb.Experiment) []int64 {
	t.Helper()
	xids := make([]int64, 0, len(exps))
	for _, exp := range exps {
		xid, err := validator.ValidateExperimentName(exp.GetName())
		if err != nil {
			t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", exp.GetName(), err)
		}
		xids = append(xids, xid)
	}
	return xids
}

// listAllPages pages through ListExperiments until the next page token is empty and returns the
// experiments of all pages in order, together with the number of pages read. It fails the test
// if a page is larger than pageSize or if paging does not finish within a generous bound.
func listAllPages(ctx context.Context, t *testing.T, repo *Repository, pageSize int32, filter, orderBy string) ([]*pb.Experiment, int) {
	t.Helper()
	const maxPages = 100
	var all []*pb.Experiment
	pageToken := ""
	for page := 1; page <= maxPages; page++ {
		got, nextToken, err := repo.ListExperiments(ctx, pageSize, pageToken, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListExperiments(pageSize=%d, filter=%q, orderBy=%q) page %d unexpected error: %v", pageSize, filter, orderBy, page, err)
		}
		if len(got) > int(pageSize) {
			t.Fatalf("ListExperiments page %d returned %d experiments, want at most %d", page, len(got), pageSize)
		}
		all = append(all, got...)
		if nextToken == "" {
			return all, page
		}
		pageToken = nextToken
	}
	t.Fatalf("ListExperiments did not finish paging within %d pages", maxPages)
	return nil, 0
}

// queryDerivedStateColumns reads, with raw SQL that bypasses the repository, the ActiveState and
// LaunchState columns of the experiment xid.
func queryDerivedStateColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid int64) derivedStateColumns {
	t.Helper()
	const query = "SELECT ActiveState, LaunchState FROM Experiments WHERE Xid = $1"
	var got derivedStateColumns
	if err := pool.QueryRow(ctx, query, xid).Scan(&got.ActiveState, &got.LaunchState); err != nil {
		t.Fatalf("QueryRow(%q, %d) unexpected error: %v", query, xid, err)
	}
	return got
}
