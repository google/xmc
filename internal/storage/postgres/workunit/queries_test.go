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

package workunit

import (
	"encoding/base64"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/internal/validator"
)

func TestRepository_ListWorkUnits(t *testing.T) {
	ctx := t.Context()
	if _, _, err := ((*Repository)(nil)).ListWorkUnits(ctx, "experiments/1", 10, "", "", "", 0); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("nil Repository.ListWorkUnits() code = %v, want FailedPrecondition", status.Code(err))
	}

	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid, otherXID = 501, 502
	postgrestest.SeedExperiment(ctx, t, essPool, xid)
	postgrestest.SeedExperiment(ctx, t, essPool, otherXID)

	fixture := []struct {
		wid          int64
		state        essmsgpb.WorkUnitStatus_WorkUnitState
		completeTime *time.Time
	}{
		{wid: 1, state: essmsgpb.WorkUnitStatus_COMPLETED, completeTime: ptrTime(baseTestTime.Add(2 * time.Hour))},
		{wid: 2, state: essmsgpb.WorkUnitStatus_FAILED, completeTime: ptrTime(baseTestTime.Add(time.Hour))},
		{wid: 3, state: essmsgpb.WorkUnitStatus_RUNNING},
		{wid: 4, state: essmsgpb.WorkUnitStatus_FAILED, completeTime: ptrTime(baseTestTime.Add(time.Hour))},
		{wid: 5, state: essmsgpb.WorkUnitStatus_COMPLETED},
	}
	for _, f := range fixture {
		wu := blankWorkUnitProto(xid, f.wid, baseTestTime.Add(time.Duration(f.wid)*time.Minute))
		wu.CurrentWorkUnitExecution.Status.State = f.state
		if f.completeTime != nil {
			wu.CurrentWorkUnitExecution.CompleteTime = timestamppb.New(*f.completeTime)
		}
		mustInsertWorkUnit(t, essPool, repo, wu)
	}
	otherWU := blankWorkUnitProto(otherXID, 1, baseTestTime)
	otherWU.CurrentWorkUnitExecution.Status.State = essmsgpb.WorkUnitStatus_FAILED
	mustInsertWorkUnit(t, essPool, repo, otherWU)

	parent := validator.ConstructExperimentName(xid)
	listAllWIDs := func(pageSize int32, filter, orderBy string) []int64 {
		var wids []int64
		pageToken := ""
		for range 20 {
			wus, nextToken, err := repo.ListWorkUnits(ctx, parent, pageSize, pageToken, filter, orderBy, 0)
			if err != nil {
				t.Fatalf("ListWorkUnits(filter=%q, orderBy=%q) failed: %v", filter, orderBy, err)
			}
			for _, wu := range wus {
				gotXID, wid, err := validator.ValidateWorkUnitName(wu.GetName())
				if err != nil || gotXID != xid {
					t.Fatalf("unexpected work unit %q (err=%v)", wu.GetName(), err)
				}
				wids = append(wids, wid)
			}
			if nextToken == "" {
				return wids
			}
			pageToken = nextToken
		}
		t.Fatal("ListWorkUnits pagination did not terminate")
		return nil
	}

	cases := []struct {
		name     string
		pageSize int32
		filter   string
		orderBy  string
		wantWIDs []int64
	}{
		{name: "default order multi-page", pageSize: 2, wantWIDs: []int64{1, 2, 3, 4, 5}},
		{name: "max int32 batch page size", pageSize: math.MaxInt32, wantWIDs: []int64{1, 2, 3, 4, 5}},
		{name: "created desc multi-page", pageSize: 2, orderBy: "created desc", wantWIDs: []int64{5, 4, 3, 2, 1}},
		{name: "work_unit_id desc", pageSize: 1, orderBy: "work_unit_id desc", wantWIDs: []int64{5, 4, 3, 2, 1}},
		{name: "state asc", pageSize: 1, orderBy: "state", wantWIDs: []int64{3, 1, 5, 2, 4}},
		{name: "state desc", pageSize: 1, orderBy: "state desc", wantWIDs: []int64{2, 4, 1, 5, 3}},
		{name: "execution_completed asc", pageSize: 1, orderBy: "execution_completed", wantWIDs: []int64{3, 5, 2, 4, 1}},
		{name: "execution_completed desc", pageSize: 1, orderBy: "execution_completed desc", wantWIDs: []int64{1, 2, 4, 3, 5}},
		{name: "filter state FAILED", pageSize: 1, filter: `state="FAILED"`, orderBy: "state", wantWIDs: []int64{2, 4}},
		{name: "or filter with state order", pageSize: 1, filter: `state = "FAILED" OR state = "COMPLETED"`, orderBy: "state", wantWIDs: []int64{1, 5, 2, 4}},
		{name: "or filter with nullable sort", pageSize: 1, filter: `state = "FAILED" OR state = "COMPLETED"`, orderBy: "execution_completed desc", wantWIDs: []int64{1, 2, 4, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.wantWIDs, listAllWIDs(tc.pageSize, tc.filter, tc.orderBy)); diff != "" {
				t.Errorf("WIDs mismatch (-want +got):\n%s", diff)
			}
		})
	}

	invalidCases := []struct {
		name      string
		parent    string
		pageSize  int32
		pageToken string
		filter    string
		orderBy   string
	}{
		{name: "zero pageSize", parent: parent, pageSize: 0},
		{name: "negative pageSize", parent: parent, pageSize: -1},
		{name: "invalid parent", parent: "invalid-parent", pageSize: 10},
		{name: "invalid filter field", parent: parent, pageSize: 10, filter: "not_a_field = 1"},
		{name: "invalid orderBy field", parent: parent, pageSize: 10, orderBy: "not_a_field desc"},
		{name: "invalid pageToken", parent: parent, pageSize: 10, pageToken: "bad-token!"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := repo.ListWorkUnits(ctx, tc.parent, tc.pageSize, tc.pageToken, tc.filter, tc.orderBy, 0)
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Errorf("ListWorkUnits(%s) code = %v (%v), want InvalidArgument", tc.name, got, err)
			}
		})
	}
}

func TestRepository_FetchActiveWorkUnitsByQueues(t *testing.T) {
	ctx := t.Context()
	if _, err := ((*Repository)(nil)).FetchActiveWorkUnitsByQueues(ctx, []string{"q"}, 10, "", storage.ActiveWorkUnitQuery{}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("nil Repository.FetchActiveWorkUnitsByQueues() code = %v, want FailedPrecondition", status.Code(err))
	}

	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	const xid = 601
	postgrestest.SeedExperiment(ctx, t, essPool, xid)

	states := []struct {
		wid   int64
		state essmsgpb.WorkUnitStatus_WorkUnitState
		pos   int32
		queue string
	}{
		{wid: 1, state: essmsgpb.WorkUnitStatus_SCHEDULING, pos: 20},
		{wid: 2, state: essmsgpb.WorkUnitStatus_SCHEDULING, pos: 10},
		{wid: 3, state: essmsgpb.WorkUnitStatus_RUNNING, pos: 0},
		{wid: 4, state: essmsgpb.WorkUnitStatus_INITIALIZING, pos: 0},
		{wid: 5, state: essmsgpb.WorkUnitStatus_STARTING, pos: 0},
		{wid: 6, state: essmsgpb.WorkUnitStatus_COMPLETED, pos: 0},
		{wid: 7, state: essmsgpb.WorkUnitStatus_SCHEDULING, pos: 15, queue: "other-queue"},
	}
	for _, s := range states {
		wu := blankWorkUnitProto(xid, s.wid, baseTestTime.Add(time.Duration(s.wid)*time.Second))
		wu.CurrentWorkUnitExecution.Status.State = s.state
		if s.queue != "" {
			wu.Executable.SchedulingConstraints.QueueName = s.queue
		}
		if s.pos > 0 {
			wu.CurrentWorkUnitExecution.Status.SchedulingInformation = &essmsgpb.StructuredMessage{
				AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
					Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{QueueInfo: &essmsgpb.QueueInfo{PositionInQueue: s.pos}},
				},
			}
		}
		mustInsertWorkUnit(t, essPool, repo, wu)
	}

	emptyRes, err := repo.FetchActiveWorkUnitsByQueues(ctx, nil, 10, "", storage.ActiveWorkUnitQuery{})
	if err != nil || len(emptyRes.WorkUnits) != 0 {
		t.Fatalf("FetchActiveWorkUnitsByQueues(nil) = (%v, %v), want 0 work units", emptyRes, err)
	}

	var gotWIDs []int64
	token := ""
	wantPages := []struct {
		count   int
		hasNext bool
	}{{2, true}, {2, true}, {1, false}}
	for i, wantPage := range wantPages {
		res, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, token, storage.ActiveWorkUnitQuery{})
		if err != nil {
			t.Fatalf("page %d FetchActiveWorkUnitsByQueues failed: %v", i+1, err)
		}
		if len(res.WorkUnits) != wantPage.count || res.TotalQueueSize != 5 || (res.NextPageToken != "") != wantPage.hasNext {
			t.Errorf("page %d = (len=%d, total=%d, hasNext=%t), want (%d, 5, %t)", i+1, len(res.WorkUnits), res.TotalQueueSize, res.NextPageToken != "", wantPage.count, wantPage.hasNext)
		}
		for _, item := range res.WorkUnits {
			if item.Author != "user" {
				t.Errorf("Author = %q, want \"user\"", item.Author)
			}
			if item.ExperimentTitle != "test" {
				t.Errorf("ExperimentTitle = %q, want \"test\"", item.ExperimentTitle)
			}
			_, wid, _ := validator.ValidateWorkUnitName(item.WorkUnit.GetName())
			gotWIDs = append(gotWIDs, wid)
		}
		token = res.NextPageToken
	}
	if diff := cmp.Diff([]int64{3, 4, 5, 2, 1}, gotWIDs); diff != "" {
		t.Errorf("active queue ordering mismatch (-want +got):\n%s", diff)
	}

	bothRes, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue", "other-queue"}, 10, "", storage.ActiveWorkUnitQuery{})
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues(both queues) failed: %v", err)
	}
	if bothRes.TotalQueueSize != 6 || bothRes.NextPageToken != "" {
		t.Errorf("both queues = (total=%d, next=%q), want (6, \"\")", bothRes.TotalQueueSize, bothRes.NextPageToken)
	}
	var gotBothWIDs []int64
	for _, item := range bothRes.WorkUnits {
		_, wid, _ := validator.ValidateWorkUnitName(item.WorkUnit.GetName())
		gotBothWIDs = append(gotBothWIDs, wid)
	}
	if diff := cmp.Diff([]int64{3, 4, 5, 2, 7, 1}, gotBothWIDs); diff != "" {
		t.Errorf("multi-queue ordering mismatch (-want +got):\n%s", diff)
	}

	rawToken := base64.RawURLEncoding.EncodeToString([]byte("3"))
	rawRes, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 0, rawToken, storage.ActiveWorkUnitQuery{})
	if err != nil || len(rawRes.WorkUnits) != 2 {
		t.Fatalf("FetchActiveWorkUnitsByQueues(rawToken) = (%v, %v), want 2 work units", rawRes, err)
	}

	for _, badToken := range []string{"!!!", base64.URLEncoding.EncodeToString([]byte("-1")), base64.URLEncoding.EncodeToString([]byte("abc"))} {
		if _, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 10, badToken, storage.ActiveWorkUnitQuery{}); !errors.Is(err, storage.ErrInvalidPageToken) {
			t.Errorf("FetchActiveWorkUnitsByQueues(token=%q) = %v, want ErrInvalidPageToken", badToken, err)
		}
	}
}

func TestRepository_FetchActiveWorkUnitsByQueues_CrossExperimentXidTieBreaker(t *testing.T) {
	ctx := t.Context()
	_, essPool := postgrestest.Setup(ctx, t)
	repo := NewRepository(essPool)

	// Insert in descending Xid order with identical (SortPhase, PositionInQueue, CreateTime, WorkUnitId)
	// so heap insertion order would return [604, 602, 601] without the Xid ASC tie-breaker.
	for _, xid := range []int64{604, 602, 601} {
		postgrestest.SeedExperiment(ctx, t, essPool, xid)
		wu := blankWorkUnitProto(xid, 1, baseTestTime)
		wu.CurrentWorkUnitExecution.Status.State = essmsgpb.WorkUnitStatus_RUNNING
		wu.Executable.SchedulingConstraints.QueueName = "tie-queue"
		mustInsertWorkUnit(t, essPool, repo, wu)
	}

	var gotNames []string
	token := ""
	for i := range 3 {
		res, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"tie-queue"}, 1, token, storage.ActiveWorkUnitQuery{})
		if err != nil {
			t.Fatalf("page %d FetchActiveWorkUnitsByQueues failed: %v", i+1, err)
		}
		for _, item := range res.WorkUnits {
			gotNames = append(gotNames, item.WorkUnit.GetName())
		}
		token = res.NextPageToken
	}
	wantNames := []string{"experiments/601/workUnits/1", "experiments/602/workUnits/1", "experiments/604/workUnits/1"}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("cross-experiment Xid tie-breaker mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractWorkUnitCursor(t *testing.T) {
	ts := timestamppb.New(baseTestTime)
	cases := []struct {
		name    string
		wu      *essmsgpb.WorkUnit
		want    map[string]any
		wantErr bool
	}{
		{
			name: "completed work unit with timestamps",
			wu: &essmsgpb.WorkUnit{
				Name:       "experiments/10/workUnits/3",
				CreateTime: ts,
				UpdateTime: ts,
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status:       &essmsgpb.WorkUnitStatus{State: essmsgpb.WorkUnitStatus_FAILED},
					CompleteTime: ts,
				},
			},
			want: map[string]any{
				"xid":                 int64(10),
				"work_unit_id":        int64(3),
				"state":               int64(essmsgpb.WorkUnitStatus_FAILED),
				"created":             baseTestTime,
				"updated":             baseTestTime,
				"execution_completed": baseTestTime,
			},
		},
		{
			name: "minimal work unit without timestamps or execution",
			wu:   &essmsgpb.WorkUnit{Name: "experiments/10/workUnits/4"},
			want: map[string]any{
				"xid":                 int64(10),
				"work_unit_id":        int64(4),
				"state":               int64(essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED),
				"execution_completed": nil,
			},
		},
		{
			name:    "invalid work unit name",
			wu:      &essmsgpb.WorkUnit{Name: "invalid"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractWorkUnitCursor(tc.wu)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ExtractWorkUnitCursor() err = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractWorkUnitCursor() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestActiveQueueOrderingAcrossPages(t *testing.T) {
	ctx := t.Context()
	_, pool := postgrestest.Setup(ctx, t)
	repo := NewRepository(pool)
	for _, xid := range []int64{100, 200} {
		postgrestest.SeedExperiment(ctx, t, pool, xid)
		author := "alice@example.com"
		if xid == 200 {
			author = "Bob@example.com"
		}
		if _, err := pool.Exec(ctx, "UPDATE Experiments SET Creator=$1 WHERE Xid=$2", author, xid); err != nil {
			t.Fatal(err)
		}
		for i, state := range []essmsgpb.WorkUnitStatus_WorkUnitState{essmsgpb.WorkUnitStatus_SCHEDULING, essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.WorkUnitStatus_INITIALIZING} {
			wu := blankWorkUnitProto(xid, int64(i+1), baseTestTime)
			wu.Priority = essmsgpb.Priority(i + 1)
			wu.CurrentWorkUnitExecution.Status.State = state
			mustInsertWorkUnit(t, pool, repo, wu)
		}
	}

	t.Run("combined filter and count", func(t *testing.T) {
		result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Author: "Bob@example.com", HasAuthor: true, Status: "RUNNING"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalQueueSize != 1 || len(result.WorkUnits) != 1 || result.NextPageToken != "" {
			t.Fatalf("filtered result = %+v", result)
		}
		if got := result.WorkUnits[0].WorkUnit.GetName(); got != "experiments/200/workUnits/2" {
			t.Errorf("work unit = %s", got)
		}
	})
	t.Run("filtered pagination", func(t *testing.T) {
		token := ""
		for page := 0; page < 3; page++ {
			result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, token, storage.ActiveWorkUnitQuery{OrderBy: "priority desc", Filter: storage.ActiveWorkUnitFilter{Author: "Bob@example.com", HasAuthor: true}})
			if err != nil {
				t.Fatal(err)
			}
			if result.TotalQueueSize != 3 || len(result.WorkUnits) != 1 {
				t.Fatalf("filtered page = %+v", result)
			}
			token = result.NextPageToken
			if (token != "") != (page < 2) {
				t.Fatalf("page %d token = %q", page, token)
			}
		}
	})
	t.Run("unmatched author", func(t *testing.T) {
		result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Author: "missing@example.com", HasAuthor: true}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalQueueSize != 0 || len(result.WorkUnits) != 0 {
			t.Fatalf("unmatched result = %+v", result)
		}
	})

	t.Run("status-only count and pagination", func(t *testing.T) {
		result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Status: "RUNNING"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalQueueSize != 2 || len(result.WorkUnits) != 1 || result.NextPageToken == "" {
			t.Fatalf("status page = %+v", result)
		}
		next, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, result.NextPageToken, storage.ActiveWorkUnitQuery{Filter: storage.ActiveWorkUnitFilter{Status: "RUNNING"}})
		if err != nil {
			t.Fatal(err)
		}
		if next.TotalQueueSize != 2 || len(next.WorkUnits) != 1 || next.NextPageToken != "" {
			t.Fatalf("status final page = %+v", next)
		}
		gotNames := []string{result.WorkUnits[0].WorkUnit.GetName(), next.WorkUnits[0].WorkUnit.GetName()}
		wantNames := []string{"experiments/100/workUnits/2", "experiments/200/workUnits/2"}
		if diff := cmp.Diff(wantNames, gotNames); diff != "" {
			t.Errorf("status pages (-want +got):\n%s", diff)
		}
	})
	t.Run("invalid ordering", func(t *testing.T) {
		if _, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 1, "", storage.ActiveWorkUnitQuery{OrderBy: "invalid"}); err == nil {
			t.Fatal("accepted invalid ordering")
		}
	})

	cases := []struct {
		order string
		want  []string
	}{
		{"author asc", []string{"experiments/200/workUnits/1", "experiments/200/workUnits/2", "experiments/200/workUnits/3", "experiments/100/workUnits/1", "experiments/100/workUnits/2", "experiments/100/workUnits/3"}},
		{"author desc", []string{"experiments/100/workUnits/1", "experiments/100/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/1", "experiments/200/workUnits/2", "experiments/200/workUnits/3"}},
		{"priority asc", []string{"experiments/100/workUnits/1", "experiments/200/workUnits/1", "experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/3"}},
		{"priority desc", []string{"experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/1", "experiments/200/workUnits/1"}},
		{"status asc", []string{"experiments/100/workUnits/2", "experiments/200/workUnits/2", "experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/1", "experiments/200/workUnits/1"}},
		{"status desc", []string{"experiments/100/workUnits/1", "experiments/200/workUnits/1", "experiments/100/workUnits/3", "experiments/200/workUnits/3", "experiments/100/workUnits/2", "experiments/200/workUnits/2"}},
	}
	for _, tt := range cases {
		t.Run(tt.order, func(t *testing.T) {
			var names []string
			token := ""
			for page := 0; page < 3; page++ {
				result, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"test-queue"}, 2, token, storage.ActiveWorkUnitQuery{OrderBy: tt.order})
				if err != nil {
					t.Fatal(err)
				}
				if result.TotalQueueSize != 6 {
					t.Fatalf("total = %d, want 6", result.TotalQueueSize)
				}
				for _, item := range result.WorkUnits {
					names = append(names, item.WorkUnit.GetName())
				}
				token = result.NextPageToken
				if page < 2 && token == "" {
					t.Fatal("missing next page")
				}
			}
			if token != "" {
				t.Fatal("unexpected final page token")
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("order = %v, want %v", names, tt.want)
			}
		})
	}
}
