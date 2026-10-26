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

package artifact

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

// lockWaitTimeout bounds how long a test waits for a backend to block on a row lock.
const lockWaitTimeout = 10 * time.Second

// ignoreACLs ignores the acls field, which the storage layer never persists.
var ignoreACLs = protocmp.IgnoreFields(&pb.Artifact{}, "acls")

// newTestRepository returns a Repository backed by a fresh ESS test database, together with the
// database's pool for raw SQL fixtures and assertions.
func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return NewRepository(essPool), essPool
}

// insertExperiment inserts the Experiments row that artifacts of experiment xid reference. Other
// repository packages own the Experiments table, so the fixture uses raw SQL.
func insertExperiment(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid int64) {
	t.Helper()
	const query = `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
VALUES ($1, 'test-title', 'test@example.com', 1, $2, $3, '', $2, $4, 0, 0, now(), now())`
	if _, err := pool.Exec(ctx, query, xid, [][]byte{}, []string{}, []byte{}); err != nil {
		t.Fatalf("failed to insert experiment %d: %v", xid, err)
	}
}

// insertArtifactChart links artifact (xid, aid) to a new chart chartID through an ArtifactCharts
// row, inserting the Charts row it references.
func insertArtifactChart(ctx context.Context, t *testing.T, pool *pgxpool.Pool, xid, aid int64, chartID string) {
	t.Helper()
	const chartQuery = `INSERT INTO Charts (ChartId, Title, Xid, Owner, CreatedAt, UpdatedAt) VALUES ($1, 'chart', $2, 'owner@example.com', now(), now())`
	if _, err := pool.Exec(ctx, chartQuery, chartID, xid); err != nil {
		t.Fatalf("failed to insert chart %s: %v", chartID, err)
	}
	const linkQuery = `INSERT INTO ArtifactCharts (Xid, ArtifactId, ArtifactChartId, ChartId, CreatedAt) VALUES ($1, $2, $3, $4, now())`
	if _, err := pool.Exec(ctx, linkQuery, xid, aid, "artifact-"+chartID, chartID); err != nil {
		t.Fatalf("failed to insert artifact chart for artifact %d: %v", aid, err)
	}
}

// countRows counts the rows of query, which must select a single count.
func countRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("QueryRow(%q) unexpected error: %v", query, err)
	}
	return n
}

// blankArtifactProto mirrors spanner.BlankArtifactProto: an experiment-level artifact aid of
// experiment xid with a URL, tags and notes.
func blankArtifactProto(xid, aid int64) *pb.Artifact {
	return &pb.Artifact{
		Name:   fmt.Sprintf("artifacts/%d", aid),
		Parent: fmt.Sprintf("experiments/%d", xid),
		Url: &pb.Url{
			Url: fmt.Sprintf("http://test-%d.com", aid),
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test-notes",
	}
}

// createArtifact stores art and fails the test on error.
func createArtifact(ctx context.Context, t *testing.T, repo *Repository, art *pb.Artifact) *pb.Artifact {
	t.Helper()
	created, err := repo.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact(%v) failed: %v", art, err)
	}
	return created
}

// artifactIDs returns the IDs of artifacts in order, or nil for no artifacts.
func artifactIDs(t *testing.T, artifacts []*pb.Artifact) []int64 {
	t.Helper()
	var ids []int64
	for _, art := range artifacts {
		var aid int64
		if _, err := fmt.Sscanf(art.GetName(), "artifacts/%d", &aid); err != nil {
			t.Fatalf("failed to parse artifact name %q: %v", art.GetName(), err)
		}
		ids = append(ids, aid)
	}
	return ids
}

// listAllPages lists artifacts page by page until the page token runs out and returns the IDs of
// every page in order.
func listAllPages(ctx context.Context, t *testing.T, repo *Repository, parent string, pageSize int32, filter, orderBy string) [][]int64 {
	t.Helper()
	var pages [][]int64
	pageToken := ""
	for {
		artifacts, nextPageToken, err := repo.ListArtifacts(ctx, parent, pageSize, pageToken, filter, orderBy, 0)
		if err != nil {
			t.Fatalf("ListArtifacts(parent=%q, pageSize=%d, pageToken=%q, filter=%q, orderBy=%q) failed: %v", parent, pageSize, pageToken, filter, orderBy, err)
		}
		pages = append(pages, artifactIDs(t, artifacts))
		if nextPageToken == "" {
			return pages
		}
		if len(artifacts) != int(pageSize) {
			t.Fatalf("ListArtifacts() returned %d artifacts with a next page token, want a full page of %d", len(artifacts), pageSize)
		}
		pageToken = nextPageToken
		if len(pages) > 100 {
			t.Fatal("ListArtifacts() pagination did not terminate")
		}
	}
}

// waitForLockWaiter blocks until a backend of the test database waits on a lock, which is how a
// repository call blocked on a row lock held by the test shows up.
func waitForLockWaiter(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const query = `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`
	deadline := time.Now().Add(lockWaitTimeout)
	for time.Now().Before(deadline) {
		if countRows(ctx, t, pool, query) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend started waiting on a row lock in time")
}

// ---------------------------------------------------------------------------
// CreateArtifact
// ---------------------------------------------------------------------------

func TestCreateArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)

	art := &pb.Artifact{
		Name:   "artifacts/456",
		Parent: "experiments/123",
		Url: &pb.Url{
			Url: "http://artifact.com",
		},
		Tags:  []string{"tag3", "tag4"},
		Notes: "artifact-notes",
	}

	createdArt, err := repo.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact failed: %v", err)
	}

	if createdArt.GetName() != art.GetName() {
		t.Errorf("CreateArtifact returned artifact with wrong name: got %q, want %q", createdArt.GetName(), art.GetName())
	}
	if createdArt.GetCreateTime() == nil {
		t.Errorf("CreateArtifact returned artifact with nil CreateTime")
	}
	if createdArt.GetUpdateTime() == nil {
		t.Errorf("CreateArtifact returned artifact with nil UpdateTime")
	}
	if !createdArt.GetCreateTime().AsTime().Equal(createdArt.GetUpdateTime().AsTime()) {
		t.Errorf("CreateArtifact CreateTime %v != UpdateTime %v", createdArt.GetCreateTime().AsTime(), createdArt.GetUpdateTime().AsTime())
	}
	if since := time.Since(createdArt.GetCreateTime().AsTime()); since < 0 || since > time.Minute {
		t.Errorf("CreateArtifact CreateTime = %v, want within a minute of now", createdArt.GetCreateTime().AsTime())
	}
	if createdArt.GetParent() != art.GetParent() {
		t.Errorf("CreateArtifact returned artifact with wrong parent: got %q, want %q", createdArt.GetParent(), art.GetParent())
	}

	// The stored row reads back exactly as returned, timestamps included.
	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform(), ignoreACLs); diff != "" {
		t.Errorf("FetchArtifact after create returned diff (-want +got):\n%s", diff)
	}
}

func TestCreateArtifact_InvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		artifact *pb.Artifact
		wantCode codes.Code
	}{
		{name: "nil_artifact", artifact: nil, wantCode: codes.Unknown},
		{name: "invalid_name", artifact: &pb.Artifact{Name: "invalid-name"}, wantCode: codes.InvalidArgument},
		{name: "missing_parent", artifact: &pb.Artifact{Name: "artifacts/1"}, wantCode: codes.InvalidArgument},
		{name: "invalid_parent", artifact: &pb.Artifact{Name: "artifacts/1", Parent: "artifacts/5"}, wantCode: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			insertExperiment(ctx, t, pool, 123)

			if _, err := repo.CreateArtifact(ctx, tt.artifact); err == nil {
				t.Fatalf("CreateArtifact(%v) succeeded, want error", tt.artifact)
			} else if got := status.Code(err); got != tt.wantCode {
				t.Errorf("CreateArtifact(%v) code = %v, want %v (err: %v)", tt.artifact, got, tt.wantCode, err)
			}
			if n := countRows(ctx, t, pool, "SELECT count(*) FROM Artifacts"); n != 0 {
				t.Errorf("Artifacts row count after failed create = %d, want 0", n)
			}
		})
	}
}

func TestCreateArtifact_WorkUnitParent(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)

	art := blankArtifactProto(123, 456)
	art.Parent = "experiments/123/workUnits/7"
	createdArt := createArtifact(ctx, t, repo, art)

	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if gotArt.GetParent() != "experiments/123/workUnits/7" {
		t.Errorf("FetchArtifact parent = %q, want %q", gotArt.GetParent(), "experiments/123/workUnits/7")
	}
	if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform(), ignoreACLs); diff != "" {
		t.Errorf("FetchArtifact returned diff (-want +got):\n%s", diff)
	}
	if n := countRows(ctx, t, pool, "SELECT count(*) FROM Artifacts WHERE Xid = 123 AND WorkUnitId = 7 AND ArtifactId = 456"); n != 1 {
		t.Errorf("rows with WorkUnitId = 7: got %d, want 1", n)
	}
}

func TestCreateArtifact_AlreadyExists(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	insertExperiment(ctx, t, pool, 124)
	createArtifact(ctx, t, repo, blankArtifactProto(123, 456))

	tests := []struct {
		name     string
		artifact *pb.Artifact
	}{
		{name: "same_experiment", artifact: blankArtifactProto(123, 456)},
		// Artifact IDs are globally unique, not merely unique within their experiment.
		{name: "other_experiment", artifact: blankArtifactProto(124, 456)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.CreateArtifact(ctx, tt.artifact)
			if !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
				t.Errorf("CreateArtifact(duplicate) error = %v, want %v", err, grpcerr.ErrAlreadyExists)
			}
		})
	}
}

func TestCreateArtifact_MissingExperiment(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	// Artifacts reference their experiment; Spanner rejects a missing parent row as well, with
	// NotFound rather than the FailedPrecondition that pgdb assigns to foreign-key violations.
	_, err := repo.CreateArtifact(ctx, blankArtifactProto(999, 1))
	if !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateArtifact(missing experiment) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
}

// storedColumns holds the derived and nullable columns of an Artifacts row, read with raw SQL.
type storedColumns struct {
	WorkUnitIDNull     bool
	Priority           int64
	Tags               []string
	LifecyclePhase     int64
	DataType           int64
	PayloadType        string
	SearchableContent  string
	AdditionalInfoNull bool
	PayloadNull        bool
}

// queryStoredColumns reads the storedColumns of artifact aid.
func queryStoredColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, aid int64) storedColumns {
	t.Helper()
	const query = `SELECT WorkUnitId IS NULL, Priority, Tags, LifecyclePhase, DataType, PayloadType, coalesce(SearchableContent, '<NULL>'),
  AdditionalInfo IS NULL, Payload IS NULL
FROM Artifacts WHERE ArtifactId = $1`
	var got storedColumns
	if err := pool.QueryRow(ctx, query, aid).Scan(&got.WorkUnitIDNull, &got.Priority, &got.Tags, &got.LifecyclePhase, &got.DataType,
		&got.PayloadType, &got.SearchableContent, &got.AdditionalInfoNull, &got.PayloadNull); err != nil {
		t.Fatalf("failed to read stored columns of artifact %d: %v", aid, err)
	}
	return got
}

func TestCreateArtifact_StoresDerivedColumns(t *testing.T) {
	tests := []struct {
		name     string
		artifact *pb.Artifact
		want     storedColumns
	}{
		{
			name:     "minimal_experiment_level_artifact",
			artifact: &pb.Artifact{Name: "artifacts/1", Parent: "experiments/123"},
			want:     storedColumns{WorkUnitIDNull: true, Tags: []string{}, AdditionalInfoNull: true, PayloadNull: true},
		},
		{
			name: "url_priority_fallback_and_work_unit_parent",
			artifact: &pb.Artifact{
				Name:   "artifacts/2",
				Parent: "experiments/123/workUnits/3",
				Url:    &pb.Url{Url: "http://a.com", Priority: 4},
				Tags:   []string{"t"},
			},
			want: storedColumns{Priority: 4, Tags: []string{"t"}, AdditionalInfoNull: true, PayloadNull: true},
		},
		{
			name: "payload_and_enums",
			artifact: &pb.Artifact{
				Name:           "artifacts/3",
				Parent:         "experiments/123",
				Priority:       2,
				Url:            &pb.Url{Priority: 9},
				LifecyclePhase: pb.Artifact_EXECUTION,
				DataType:       pb.Artifact_TEXT,
				AdditionalInfo: &pb.Artifact_AdditionalInfo{Title: "info"},
				Payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_CodeSource{CodeSource: &pb.CodeSourceArtifact{
					Title:     "code-title",
					CodeBlock: "print('hi')",
				}}},
			},
			want: storedColumns{
				WorkUnitIDNull:    true,
				Priority:          2,
				Tags:              []string{},
				LifecyclePhase:    int64(pb.Artifact_EXECUTION),
				DataType:          int64(pb.Artifact_TEXT),
				PayloadType:       "code_source",
				SearchableContent: "code-title print('hi')",
			},
		},
		{
			name:     "empty_payload_message_is_stored_as_present",
			artifact: &pb.Artifact{Name: "artifacts/4", Parent: "experiments/123", Payload: &pb.ArtifactPayload{}},
			want:     storedColumns{WorkUnitIDNull: true, Tags: []string{}, AdditionalInfoNull: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			insertExperiment(ctx, t, pool, 123)
			createArtifact(ctx, t, repo, tt.artifact)

			aid := artifactIDs(t, []*pb.Artifact{tt.artifact})[0]
			if diff := cmp.Diff(tt.want, queryStoredColumns(ctx, t, pool, aid)); diff != "" {
				t.Errorf("stored columns mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FetchArtifact
// ---------------------------------------------------------------------------

func TestFetchArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)

	art := &pb.Artifact{
		Name:   "artifacts/456",
		Parent: "experiments/123",
		Url: &pb.Url{
			Url: "http://artifact.com",
		},
		Tags: []string{"tag1", "tag2"},
	}
	createdArt := createArtifact(ctx, t, repo, art)

	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}

	if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform(), ignoreACLs); diff != "" {
		t.Errorf("FetchArtifact(456) returned diff (-want +got):\n%s", diff)
	}
}

func TestFetchArtifact_RoundTripsAllFields(t *testing.T) {
	tests := []struct {
		name     string
		artifact *pb.Artifact
	}{
		{
			name: "all_fields",
			artifact: &pb.Artifact{
				Name:   "artifacts/456",
				Parent: "experiments/123/workUnits/2",
				Url: &pb.Url{
					Url:         "http://artifact.com",
					Icon:        "icon.png",
					DisplayName: "display",
					Priority:    5,
				},
				Priority:       5,
				Tags:           []string{"tag1", "tag2"},
				Notes:          "notes",
				LifecyclePhase: pb.Artifact_OUTPUT,
				DataType:       pb.Artifact_IMAGE,
				MimeType:       "image/png",
				AdditionalInfo: &pb.Artifact_AdditionalInfo{Title: "info", Content: &pb.Artifact_AdditionalInfo_Text{Text: "text"}},
				Payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{
					DisplayName: "xprof-display",
					RunGroup:    "xprof-run-group",
				}}},
			},
		},
		{
			name: "empty_proto_fields_stay_present",
			artifact: &pb.Artifact{
				Name:           "artifacts/457",
				Parent:         "experiments/123",
				Url:            &pb.Url{},
				AdditionalInfo: &pb.Artifact_AdditionalInfo{},
				Payload:        &pb.ArtifactPayload{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			insertExperiment(ctx, t, pool, 123)
			createdArt := createArtifact(ctx, t, repo, tt.artifact)

			gotArt, err := repo.FetchArtifact(ctx, artifactIDs(t, []*pb.Artifact{tt.artifact})[0])
			if err != nil {
				t.Fatalf("FetchArtifact failed: %v", err)
			}
			if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform(), ignoreACLs); diff != "" {
				t.Errorf("FetchArtifact returned diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchArtifact_NilURLReadsBackEmpty(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	createArtifact(ctx, t, repo, &pb.Artifact{Name: "artifacts/1", Parent: "experiments/123", Priority: 3})

	// The Url columns are NOT NULL, so a missing Url is stored as empty strings and, like the
	// Spanner row conversion, always reads back as a Url message carrying the stored priority.
	gotArt, err := repo.FetchArtifact(ctx, 1)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	want := &pb.Url{Priority: 3}
	if diff := cmp.Diff(want, gotArt.GetUrl(), protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact url mismatch (-want +got):\n%s", diff)
	}
	if gotArt.GetPriority() != 3 {
		t.Errorf("FetchArtifact priority = %d, want 3", gotArt.GetPriority())
	}
}

func TestFetchArtifact_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	_, err := repo.FetchArtifact(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("FetchArtifact for non-existent AID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

// ---------------------------------------------------------------------------
// ListArtifacts
// ---------------------------------------------------------------------------

func TestListArtifacts(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	var artifacts []*pb.Artifact
	for i := 1; i < 6; i++ {
		art := blankArtifactProto(1, int64(i))
		art.Url.DisplayName = fmt.Sprintf("display-%d", i)
		art.Notes = "test-notes"
		art.Tags = []string{"tag1", "tag2"}
		artifacts = append(artifacts, createArtifact(ctx, t, repo, art))
	}

	gotArtifacts, nextPageToken, err := repo.ListArtifacts(ctx, "", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if nextPageToken != "" {
		t.Errorf("ListArtifacts returned next page token %q, want empty", nextPageToken)
	}
	if len(gotArtifacts) != 5 {
		t.Errorf("ListArtifacts returned %d artifacts, want 5", len(gotArtifacts))
	}

	// Sort artifacts by AID for comparison.
	sort.Slice(artifacts, func(i, j int) bool { return artifactIDs(t, artifacts)[i] < artifactIDs(t, artifacts)[j] })
	sort.Slice(gotArtifacts, func(i, j int) bool { return artifactIDs(t, gotArtifacts)[i] < artifactIDs(t, gotArtifacts)[j] })
	if diff := cmp.Diff(artifacts, gotArtifacts, protocmp.Transform(), ignoreACLs); diff != "" {
		t.Errorf("ListArtifacts() returned diff (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_Empty(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	gotArtifacts, nextPageToken, err := repo.ListArtifacts(ctx, "experiments/1", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 0 || nextPageToken != "" {
		t.Errorf("ListArtifacts() = %v, %q; want no artifacts and an empty page token", gotArtifacts, nextPageToken)
	}
}

func TestListArtifacts_Pagination(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	for i := 1; i < 6; i++ {
		createArtifact(ctx, t, repo, blankArtifactProto(1, int64(i)))
	}

	tests := []struct {
		pageSize   int
		wantCounts []int
	}{
		{3, []int{3, 2}},
		{5, []int{5}},
		{1, []int{1, 1, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("pageSize=%d", tc.pageSize), func(t *testing.T) {
			var gotCounts []int
			var pageToken string
			for i := 0; i < len(tc.wantCounts); i++ {
				artifacts, nextPageToken, err := repo.ListArtifacts(ctx, "", int32(tc.pageSize), pageToken, "", "artifact_id", 0)
				if err != nil {
					t.Fatalf("ListArtifacts failed: %v", err)
				}
				gotCounts = append(gotCounts, len(artifacts))
				pageToken = nextPageToken
				if diff := cmp.Diff(tc.wantCounts[i], len(artifacts)); diff != "" {
					t.Errorf("ListArtifacts returned unexpected page count (-want +got):\n%s", diff)
				}
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListArtifacts returned empty nextPageToken on page %d, want non-empty", i+1)
				}
			}
			if pageToken != "" {
				t.Errorf("ListArtifacts returned non-empty nextPageToken on last page, want empty")
			}
			if diff := cmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListArtifacts returned unexpected page counts (-want +got):\n%s", diff)
			}
		})
	}
}

// The default ordering of the Artifacts TableView (xid, work_unit_id NULLS FIRST, artifact_id, all
// ascending) must hold across page boundaries when no orderBy is given.
func TestListArtifacts_DefaultOrderAcrossPages(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	// Create in shuffled ID order so that insertion order cannot masquerade as the default order.
	for _, aid := range []int64{5, 2, 7, 1, 6, 3, 4} {
		createArtifact(ctx, t, repo, blankArtifactProto(1, aid))
	}

	tests := []struct {
		name     string
		parent   string
		pageSize int32
		want     [][]int64
	}{
		{name: "all_experiments", parent: "", pageSize: 3, want: [][]int64{{1, 2, 3}, {4, 5, 6}, {7}}},
		{name: "experiment_parent", parent: "experiments/1", pageSize: 3, want: [][]int64{{1, 2, 3}, {4, 5, 6}, {7}}},
		{name: "page_size_larger_than_collection", parent: "experiments/1", pageSize: 10, want: [][]int64{{1, 2, 3, 4, 5, 6, 7}}},
		{name: "page_size_one", parent: "experiments/1", pageSize: 1, want: [][]int64{{1}, {2}, {3}, {4}, {5}, {6}, {7}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, listAllPages(ctx, t, repo, tt.parent, tt.pageSize, "", "")); diff != "" {
				t.Errorf("ListArtifacts pages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Experiment-level and work-unit-level artifacts share the Artifacts table; the parent decides
// which of them a list returns, and the NULL WorkUnitId of experiment-level artifacts sorts first
// and must survive the page-token round trip.
func TestListArtifacts_ParentScoping(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)
	insertExperiment(ctx, t, pool, 2)

	parents := map[int64]string{
		1: "experiments/1",
		2: "experiments/1",
		3: "experiments/1/workUnits/1",
		4: "experiments/1/workUnits/2",
		5: "experiments/2",
		6: "experiments/2/workUnits/1",
	}
	// Create in reverse so that insertion order differs from the default order.
	for aid := int64(6); aid >= 1; aid-- {
		art := blankArtifactProto(1, aid)
		art.Parent = parents[aid]
		art.Tags = []string{fmt.Sprintf("tag-%d", aid%2)}
		createArtifact(ctx, t, repo, art)
	}

	tests := []struct {
		name     string
		parent   string
		filter   string
		pageSize int32
		want     [][]int64
	}{
		{name: "all", parent: "", pageSize: 10, want: [][]int64{{1, 2, 3, 4, 5, 6}}},
		{name: "experiment_includes_work_unit_artifacts", parent: "experiments/1", pageSize: 10, want: [][]int64{{1, 2, 3, 4}}},
		{name: "work_unit", parent: "experiments/1/workUnits/1", pageSize: 10, want: [][]int64{{3}}},
		{name: "other_work_unit", parent: "experiments/1/workUnits/2", pageSize: 10, want: [][]int64{{4}}},
		{name: "work_unit_without_artifacts", parent: "experiments/1/workUnits/3", pageSize: 10, want: [][]int64{nil}},
		{name: "other_experiment", parent: "experiments/2", pageSize: 10, want: [][]int64{{5, 6}}},
		{name: "experiment_with_filter", parent: "experiments/1", filter: `tags:"tag-0"`, pageSize: 10, want: [][]int64{{2, 4}}},
		{name: "work_unit_with_filter", parent: "experiments/1/workUnits/1", filter: `tags:"tag-0"`, pageSize: 10, want: [][]int64{nil}},
		{name: "paginated_across_null_work_unit_boundary", parent: "experiments/1", pageSize: 1, want: [][]int64{{1}, {2}, {3}, {4}}},
		{name: "paginated_across_experiments", parent: "", pageSize: 4, want: [][]int64{{1, 2, 3, 4}, {5, 6}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, listAllPages(ctx, t, repo, tt.parent, tt.pageSize, tt.filter, "")); diff != "" {
				t.Errorf("ListArtifacts pages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListArtifacts_InvalidArguments(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)
	createArtifact(ctx, t, repo, blankArtifactProto(1, 1))

	tests := []struct {
		name      string
		parent    string
		pageSize  int32
		pageToken string
		filter    string
		orderBy   string
		wantCode  codes.Code
	}{
		{name: "invalid_parent", parent: "artifacts/1", pageSize: 10, wantCode: codes.InvalidArgument},
		{name: "invalid_page_token", pageSize: 10, pageToken: "not-a-token", wantCode: codes.InvalidArgument},
		{name: "unknown_order_by_field", pageSize: 10, orderBy: "payload_type", wantCode: codes.InvalidArgument},
		{name: "invalid_order_by_direction", pageSize: 10, orderBy: "created sideways", wantCode: codes.InvalidArgument},
		{name: "unknown_filter_field", pageSize: 10, filter: `nonexistent="x"`, wantCode: codes.InvalidArgument},
		{name: "zero_page_size", pageSize: 0, wantCode: codes.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := repo.ListArtifacts(ctx, tt.parent, tt.pageSize, tt.pageToken, tt.filter, tt.orderBy, 0)
			if err == nil {
				t.Fatal("ListArtifacts() succeeded, want error")
			}
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("ListArtifacts() code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}
		})
	}
}

func TestListArtifacts_Filter(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	art1 := blankArtifactProto(1, 1)
	art1.Url.DisplayName = "display-1"
	createArtifact(ctx, t, repo, art1)
	art2 := blankArtifactProto(1, 2)
	art2.Url.DisplayName = "display-2"
	createArtifact(ctx, t, repo, art2)

	gotArtifacts, _, err := repo.ListArtifacts(ctx, "", 10, "", `display_name="display-1"`, "", 0)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 1 {
		t.Fatalf("ListArtifacts with filter returned %d entries, want 1", len(gotArtifacts))
	}
	if gotArtifacts[0].GetUrl().GetDisplayName() != "display-1" {
		t.Errorf("ListArtifacts with filter returned entry with display name %q, want display-1", gotArtifacts[0].GetUrl().GetDisplayName())
	}
}

func TestListArtifacts_FilterByPayloadType(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	// One managed-xprof artifact and one code-source artifact, so the filter is exercised against
	// a different payload type rather than merely nil payload.
	xprofArt := blankArtifactProto(1, 1)
	xprofArt.Url.DisplayName = "xprof-art"
	xprofArt.Payload = &pb.ArtifactPayload{
		Type: &pb.ArtifactPayload_ManagedXprof{
			ManagedXprof: &pb.ManagedXprofArtifact{
				DisplayName: "xprof-display",
				RunGroup:    "xprof-run-group",
			},
		},
	}
	createArtifact(ctx, t, repo, xprofArt)

	codeArt := blankArtifactProto(1, 2)
	codeArt.Url.DisplayName = "code-art"
	codeArt.Payload = &pb.ArtifactPayload{
		Type: &pb.ArtifactPayload_CodeSource{
			CodeSource: &pb.CodeSourceArtifact{
				Title:     "code-title",
				CodeBlock: "print('hi')",
			},
		},
	}
	createArtifact(ctx, t, repo, codeArt)

	noPayloadArt := blankArtifactProto(1, 3)
	noPayloadArt.Url.DisplayName = "plain-art"
	createArtifact(ctx, t, repo, noPayloadArt)

	tests := []struct {
		name             string
		filter           string
		wantDisplayNames []string
	}{
		{
			name:             "payload_type managed_xprof returns only xprof artifact",
			filter:           `payload_type = "managed_xprof"`,
			wantDisplayNames: []string{"xprof-art"},
		},
		{
			name:             "payload_type code_source returns only code-source artifact",
			filter:           `payload_type = "code_source"`,
			wantDisplayNames: []string{"code-art"},
		},
		{
			name:             "payload_type text matches nothing",
			filter:           `payload_type = "text"`,
			wantDisplayNames: nil,
		},
		{
			// An unset filter must not restrict results: every artifact is returned.
			name:             "empty filter returns all artifacts",
			filter:           "",
			wantDisplayNames: []string{"code-art", "plain-art", "xprof-art"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := repo.ListArtifacts(ctx, "", 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListArtifacts(filter=%q) failed: %v", tc.filter, err)
			}
			var gotNames []string
			for _, a := range got {
				gotNames = append(gotNames, a.GetUrl().GetDisplayName())
			}
			// No orderBy is requested, so compare as a sorted set to stay robust against result
			// ordering.
			sort.Strings(gotNames)
			want := append([]string(nil), tc.wantDisplayNames...)
			sort.Strings(want)
			if diff := cmp.Diff(want, gotNames); diff != "" {
				t.Errorf("ListArtifacts(filter=%q) display names mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListArtifacts_OrderBy(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	var aids []int64
	for i := 1; i < 4; i++ {
		art := blankArtifactProto(1, int64(i))
		art.Url.Priority = int32(10 - i) // 9,8,7
		created := createArtifact(ctx, t, repo, art)
		aids = append(aids, artifactIDs(t, []*pb.Artifact{created})[0])
	}

	gotArtifacts, _, err := repo.ListArtifacts(ctx, "", 10, "", "", "priority desc", 0)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 3 {
		t.Fatalf("ListArtifacts with orderby returned %d artifacts, want 3", len(gotArtifacts))
	}

	if diff := cmp.Diff(aids, artifactIDs(t, gotArtifacts)); diff != "" {
		t.Errorf("ListArtifacts with orderBy='priority desc' returned artifacts in wrong order (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_Complex(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	testData := []struct {
		displayName string
		mimeType    string
	}{
		{"D", "M1"},
		{"C", "M2"},
		{"B", "M1"},
		{"A", "M2"},
		{"E", "M1"},
	}
	artifacts := make([]*pb.Artifact, len(testData))
	for i, d := range testData {
		art := blankArtifactProto(1, int64(i+1))
		art.Url.DisplayName = d.displayName
		art.MimeType = d.mimeType
		artifacts[i] = createArtifact(ctx, t, repo, art)
	}

	// List page 1: filter by mime_type=M1, order by display_name desc, page size 2.
	filter := `mime_type="M1"`
	orderBy := "display_name desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := repo.ListArtifacts(ctx, "", pageSize, "", filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListArtifacts page 1 failed: %v", err)
	}
	if page1Token == "" {
		t.Errorf("ListArtifacts page 1 got empty next page token, want non-empty")
	}

	wantPage1 := []*pb.Artifact{artifacts[4], artifacts[0]} // E, D
	cmpOpts := []cmp.Option{protocmp.Transform(), ignoreACLs}
	if diff := cmp.Diff(wantPage1, gotPage1, cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts page 1 returned diff (-want +got):\n%s", diff)
	}

	// List page 2.
	gotPage2, page2Token, err := repo.ListArtifacts(ctx, "", pageSize, page1Token, filter, orderBy, 0)
	if err != nil {
		t.Fatalf("ListArtifacts page 2 failed: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListArtifacts page 2 got next page token %q, want empty", page2Token)
	}
	wantPage2 := []*pb.Artifact{artifacts[2]} // B
	if diff := cmp.Diff(wantPage2, gotPage2, cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_FilterFields(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	arts := []*pb.Artifact{
		blankArtifactProto(1, 1),
		blankArtifactProto(1, 2),
		blankArtifactProto(1, 3),
	}
	arts[0].Url.DisplayName = "name-1"
	arts[0].Tags = []string{"tag1"}
	arts[0].Notes = "notes-1"
	arts[0].MimeType = "mime-1"
	arts[0].LifecyclePhase = pb.Artifact_INPUT
	arts[0].DataType = pb.Artifact_TEXT
	arts[0].Payload = &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{Title: "readme", Text: "hello profile world"}}}
	arts[1].Url.DisplayName = "name-2"
	arts[1].Tags = []string{"tag2"}
	arts[1].Notes = "notes-2"
	arts[1].MimeType = "mime-2"
	arts[1].LifecyclePhase = pb.Artifact_EXECUTION
	arts[1].DataType = pb.Artifact_IMAGE
	arts[1].Priority = 7
	arts[2].Url.DisplayName = "name-1"
	arts[2].Tags = []string{"tag1", "tag3", "profile"}
	arts[2].Notes = "notes-3"
	arts[2].MimeType = "mime-3"
	arts[2].LifecyclePhase = pb.Artifact_EXECUTION
	arts[2].DataType = pb.Artifact_TEXT

	for _, art := range arts {
		createArtifact(ctx, t, repo, art)
	}

	testCases := []struct {
		name   string
		filter string
		want   []int64 // aids of expected artifacts
	}{
		{"artifact_id", "artifact_id=1", []int64{1}},
		{"artifact_id_range", "artifact_id>1", []int64{2, 3}},
		{"xid", "xid=1", []int64{1, 2, 3}},
		{"display_name", `display_name="name-1"`, []int64{1, 3}},
		{"tags", `tags:"tag1"`, []int64{1, 3}},
		{"not_tags", `-tags:"tag1"`, []int64{2}},
		{"notes", `notes="notes-3"`, []int64{3}},
		{"mime_type", `mime_type="mime-2"`, []int64{2}},
		{"priority", "priority=7", []int64{2}},
		{"lifecycle_phase_symbolic", "lifecycle_phase=EXECUTION", []int64{2, 3}},
		{"lifecycle_phase_quoted", `lifecycle_phase="INPUT"`, []int64{1}},
		{"data_type", `data_type="text"`, []int64{1, 3}},
		{"conjunction", `lifecycle_phase=EXECUTION AND data_type=TEXT`, []int64{3}},
		{"disjunction", `notes="notes-1" OR notes="notes-2"`, []int64{1, 2}},
		// A bare term is a full-text search over the *_Tokens columns: it matches the payload's
		// searchable content of artifact 1 and a tag of artifact 3.
		{"full_text_search", `"profile"`, []int64{1, 3}},
		{"full_text_search_notes", `notes-2`, []int64{2}},
		{"no_match", `notes="missing"`, nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotArtifacts, _, err := repo.ListArtifacts(ctx, "", 10, "", tc.filter, "artifact_id", 0)
			if err != nil {
				t.Fatalf("ListArtifacts failed: %v", err)
			}
			var gotAIDs []int64
			if len(gotArtifacts) > 0 {
				gotAIDs = artifactIDs(t, gotArtifacts)
			}
			if diff := cmp.Diff(tc.want, gotAIDs); diff != "" {
				t.Errorf("ListArtifacts with filter %q returned diff (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListArtifacts_OrderByFields(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	aids := []int64{3, 1, 2}
	for _, aid := range aids {
		createArtifact(ctx, t, repo, blankArtifactProto(1, aid))
		// Keep the microsecond CreateTime and UpdateTime values of consecutive artifacts distinct.
		time.Sleep(time.Millisecond)
	}

	testCases := []struct {
		name    string
		orderBy string
		want    []int64 // aids of expected artifacts in order
	}{
		{"artifact_id asc", "artifact_id", []int64{1, 2, 3}},
		{"artifact_id desc", "artifact_id desc", []int64{3, 2, 1}},
		{"created asc", "created", []int64{3, 1, 2}},
		{"created desc", "created desc", []int64{2, 1, 3}},
		{"updated asc", "updated", []int64{3, 1, 2}},
		{"updated desc", "updated desc", []int64{2, 1, 3}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotArtifacts, _, err := repo.ListArtifacts(ctx, "", 10, "", "", tc.orderBy, 0)
			if err != nil {
				t.Fatalf("ListArtifacts failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, artifactIDs(t, gotArtifacts)); diff != "" {
				t.Errorf("ListArtifacts with orderBy %q returned diff (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

// User orderings with ties and timestamp cursors must paginate without skipping or repeating
// artifacts; ties fall back to the default ordering.
func TestListArtifacts_OrderByAcrossPages(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 1)

	// aid: display_name / priority
	fixtures := []struct {
		aid         int64
		displayName string
		priority    int32
	}{
		{1, "B", 2},
		{2, "A", 1},
		{3, "B", 2},
		{4, "A", 3},
		{5, "C", 1},
	}
	for _, f := range fixtures {
		art := blankArtifactProto(1, f.aid)
		art.Url.DisplayName = f.displayName
		art.Priority = f.priority
		createArtifact(ctx, t, repo, art)
		time.Sleep(time.Millisecond)
	}

	tests := []struct {
		name     string
		orderBy  string
		pageSize int32
		want     [][]int64
	}{
		{name: "display_name_asc_with_ties", orderBy: "display_name", pageSize: 2, want: [][]int64{{2, 4}, {1, 3}, {5}}},
		{name: "display_name_desc_with_ties", orderBy: "display_name desc", pageSize: 2, want: [][]int64{{5, 1}, {3, 2}, {4}}},
		{name: "priority_desc_with_ties", orderBy: "priority desc", pageSize: 2, want: [][]int64{{4, 1}, {3, 2}, {5}}},
		{name: "priority_asc_then_display_name_desc", orderBy: "priority, display_name desc", pageSize: 2, want: [][]int64{{5, 2}, {1, 3}, {4}}},
		{name: "created_desc", orderBy: "created desc", pageSize: 2, want: [][]int64{{5, 4}, {3, 2}, {1}}},
		{name: "updated_asc", orderBy: "updated", pageSize: 3, want: [][]int64{{1, 2, 3}, {4, 5}}},
		{name: "artifact_id_desc", orderBy: "artifact_id desc", pageSize: 2, want: [][]int64{{5, 4}, {3, 2}, {1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, listAllPages(ctx, t, repo, "experiments/1", tt.pageSize, "", tt.orderBy)); diff != "" {
				t.Errorf("ListArtifacts pages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpdateArtifact
// ---------------------------------------------------------------------------

func TestUpdateArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	// 1. Create an Experiment and Artifact.
	xid := int64(123)
	aid := int64(456)
	insertExperiment(ctx, t, pool, xid)
	createdArt := createArtifact(ctx, t, repo, blankArtifactProto(xid, aid))

	// 2. Update the Artifact.
	update := &storage.ArtifactUpdate{
		AID: aid,
		Artifact: &pb.Artifact{
			Notes: "new-notes",
			Tags:  []string{"tag3"},
			Url:   &pb.Url{Url: "http://new.com"},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes", "tags", "url"}},
	}
	res, err := repo.UpdateArtifact(ctx, update)
	if err != nil {
		t.Fatalf("UpdateArtifact failed: %v", err)
	}
	originalArt, updatedArt := res.Original, res.Updated

	// 3. Verify the update.
	if diff := cmp.Diff(createdArt, originalArt, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact returned unexpected original artifact: diff (-want +got):\n%s", diff)
	}
	if updatedArt.GetNotes() != "new-notes" {
		t.Errorf("UpdateArtifact returned artifact with wrong notes: got %q, want %q", updatedArt.GetNotes(), "new-notes")
	}
	if diff := cmp.Diff([]string{"tag3"}, updatedArt.GetTags(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact returned artifact with wrong tags: diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(&pb.Url{Url: "http://new.com"}, updatedArt.GetUrl(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact returned artifact with wrong url: diff (-want +got):\n%s", diff)
	}
	if !updatedArt.UpdateTime.AsTime().After(createdArt.UpdateTime.AsTime()) {
		t.Errorf("UpdateArtifact did not update UpdateTime: got %v, want > %v", updatedArt.UpdateTime, createdArt.UpdateTime)
	}
	if !updatedArt.CreateTime.AsTime().Equal(createdArt.CreateTime.AsTime()) {
		t.Errorf("UpdateArtifact changed CreateTime: got %v, want %v", updatedArt.CreateTime, createdArt.CreateTime)
	}
	if updatedArt.GetParent() != createdArt.GetParent() {
		t.Errorf("UpdateArtifact unexpectedly updated Parent: got %q, want %q", updatedArt.GetParent(), createdArt.GetParent())
	}

	// 4. Verify with FetchArtifact.
	gotArt, err := repo.FetchArtifact(ctx, aid)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if diff := cmp.Diff(updatedArt, gotArt, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateArtifact_FieldPaths(t *testing.T) {
	base := func() *pb.Artifact {
		art := blankArtifactProto(123, 456)
		art.Url = &pb.Url{Url: "http://old.com", DisplayName: "old", Priority: 2}
		art.Priority = 2
		art.MimeType = "text/old"
		art.LifecyclePhase = pb.Artifact_INPUT
		art.DataType = pb.Artifact_URL
		art.AdditionalInfo = &pb.Artifact_AdditionalInfo{Title: "old-info"}
		art.Payload = &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{Title: "old", Text: "old text"}}}
		return art
	}
	tests := []struct {
		name   string
		update *pb.Artifact
		paths  []string
		// mutate applies the expected change to a copy of the created artifact.
		mutate      func(want *pb.Artifact)
		wantColumns storedColumns
	}{
		{
			name:   "priority_also_sets_url_priority",
			update: &pb.Artifact{Priority: 9},
			paths:  []string{"priority"},
			mutate: func(want *pb.Artifact) {
				want.Priority = 9
				want.Url.Priority = 9
			},
			wantColumns: storedColumns{WorkUnitIDNull: true, Priority: 9, Tags: []string{"tag1", "tag2"}, LifecyclePhase: int64(pb.Artifact_INPUT), DataType: int64(pb.Artifact_URL), PayloadType: "text", SearchableContent: "old old text"},
		},
		{
			name:   "url_replaces_url_and_priority",
			update: &pb.Artifact{Url: &pb.Url{Url: "http://new.com", Priority: 5}},
			paths:  []string{"url"},
			mutate: func(want *pb.Artifact) {
				want.Url = &pb.Url{Url: "http://new.com", Priority: 5}
				want.Priority = 5
			},
			wantColumns: storedColumns{WorkUnitIDNull: true, Priority: 5, Tags: []string{"tag1", "tag2"}, LifecyclePhase: int64(pb.Artifact_INPUT), DataType: int64(pb.Artifact_URL), PayloadType: "text", SearchableContent: "old old text"},
		},
		{
			name: "payload_refreshes_derived_columns",
			update: &pb.Artifact{Payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{
				DisplayName: "xprof-display",
				RunGroup:    "xprof-run-group",
			}}}},
			paths: []string{"payload"},
			mutate: func(want *pb.Artifact) {
				want.Payload = &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{
					DisplayName: "xprof-display",
					RunGroup:    "xprof-run-group",
				}}}
			},
			wantColumns: storedColumns{WorkUnitIDNull: true, Priority: 2, Tags: []string{"tag1", "tag2"}, LifecyclePhase: int64(pb.Artifact_INPUT), DataType: int64(pb.Artifact_URL), PayloadType: "managed_xprof", SearchableContent: "xprof-display xprof-run-group"},
		},
		{
			name:   "clearing_payload_and_additional_info_stores_NULL",
			update: &pb.Artifact{},
			paths:  []string{"payload", "additional_info"},
			mutate: func(want *pb.Artifact) {
				want.Payload = nil
				want.AdditionalInfo = nil
			},
			wantColumns: storedColumns{WorkUnitIDNull: true, Priority: 2, Tags: []string{"tag1", "tag2"}, LifecyclePhase: int64(pb.Artifact_INPUT), DataType: int64(pb.Artifact_URL), AdditionalInfoNull: true, PayloadNull: true},
		},
		{
			name: "enums_mime_type_notes_tags_and_additional_info",
			update: &pb.Artifact{
				LifecyclePhase: pb.Artifact_OUTPUT,
				DataType:       pb.Artifact_METRICS,
				MimeType:       "application/json",
				Notes:          "new-notes",
				Tags:           []string{},
				AdditionalInfo: &pb.Artifact_AdditionalInfo{Title: "new-info"},
			},
			paths: []string{"lifecycle_phase", "data_type", "mime_type", "notes", "tags", "additional_info"},
			mutate: func(want *pb.Artifact) {
				want.LifecyclePhase = pb.Artifact_OUTPUT
				want.DataType = pb.Artifact_METRICS
				want.MimeType = "application/json"
				want.Notes = "new-notes"
				want.Tags = nil
				want.AdditionalInfo = &pb.Artifact_AdditionalInfo{Title: "new-info"}
			},
			wantColumns: storedColumns{WorkUnitIDNull: true, Priority: 2, Tags: []string{}, LifecyclePhase: int64(pb.Artifact_OUTPUT), DataType: int64(pb.Artifact_METRICS), PayloadType: "text", SearchableContent: "old old text"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			insertExperiment(ctx, t, pool, 123)
			createdArt := createArtifact(ctx, t, repo, base())

			res, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{AID: 456, Artifact: tt.update, UpdateMask: &fmpb.FieldMask{Paths: tt.paths}})
			if err != nil {
				t.Fatalf("UpdateArtifact failed: %v", err)
			}

			want := proto.Clone(createdArt).(*pb.Artifact)
			tt.mutate(want)
			ignoreUpdateTime := protocmp.IgnoreFields(&pb.Artifact{}, "update_time")
			if diff := cmp.Diff(want, res.Updated, protocmp.Transform(), ignoreUpdateTime); diff != "" {
				t.Errorf("UpdateArtifact updated artifact mismatch (-want +got):\n%s", diff)
			}
			if !res.Updated.GetUpdateTime().AsTime().After(createdArt.GetUpdateTime().AsTime()) {
				t.Errorf("UpdateArtifact UpdateTime = %v, want after %v", res.Updated.GetUpdateTime().AsTime(), createdArt.GetUpdateTime().AsTime())
			}
			gotArt, err := repo.FetchArtifact(ctx, 456)
			if err != nil {
				t.Fatalf("FetchArtifact failed: %v", err)
			}
			if diff := cmp.Diff(res.Updated, gotArt, protocmp.Transform()); diff != "" {
				t.Errorf("FetchArtifact after update mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantColumns, queryStoredColumns(ctx, t, pool, 456)); diff != "" {
				t.Errorf("stored columns after update mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUpdateArtifact_NoChange(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	createdArt := createArtifact(ctx, t, repo, blankArtifactProto(123, 456))

	// Writing the values the artifact already has is a no-op that leaves UpdateTime untouched.
	res, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
		AID:        456,
		Artifact:   &pb.Artifact{Notes: "test-notes", Tags: []string{"tag1", "tag2"}},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes", "tags"}},
	})
	if err != nil {
		t.Fatalf("UpdateArtifact failed: %v", err)
	}
	if diff := cmp.Diff(createdArt, res.Original, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact original mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(createdArt, res.Updated, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact updated mismatch for a no-op update (-want +got):\n%s", diff)
	}
	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact after no-op update mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateArtifact_Errors(t *testing.T) {
	tests := []struct {
		name     string
		update   *storage.ArtifactUpdate
		wantErr  error
		wantCode codes.Code
	}{
		{name: "nil_update", update: nil, wantCode: codes.Unknown},
		{
			name: "immutable_field",
			update: &storage.ArtifactUpdate{
				AID:        456,
				Artifact:   &pb.Artifact{Name: "artifacts/789"},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"name"}},
			},
			wantErr:  grpcerr.ErrInvalidArgument,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "not_found",
			update: &storage.ArtifactUpdate{
				AID:        123456789,
				Artifact:   &pb.Artifact{Notes: "new-notes"},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
			},
			wantErr:  grpcerr.ErrNotFound,
			wantCode: codes.NotFound,
		},
		{
			name: "empty_mask",
			update: &storage.ArtifactUpdate{
				AID:        456,
				Artifact:   &pb.Artifact{},
				UpdateMask: &fmpb.FieldMask{Paths: []string{}},
			},
			wantErr:  grpcerr.ErrInvalidArgument,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "nil_artifact",
			update: &storage.ArtifactUpdate{
				AID:        456,
				UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
			},
			wantErr:  grpcerr.ErrInvalidArgument,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "nil_url",
			update: &storage.ArtifactUpdate{
				AID:        456,
				Artifact:   &pb.Artifact{},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"url"}},
			},
			wantErr:  grpcerr.ErrInvalidArgument,
			wantCode: codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			insertExperiment(ctx, t, pool, 123)
			createdArt := createArtifact(ctx, t, repo, blankArtifactProto(123, 456))

			_, err := repo.UpdateArtifact(ctx, tt.update)
			if err == nil {
				t.Fatal("UpdateArtifact() succeeded, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("UpdateArtifact() error = %v, want %v", err, tt.wantErr)
			}
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("UpdateArtifact() code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}

			// A failed update leaves the artifact untouched.
			gotArt, err := repo.FetchArtifact(ctx, 456)
			if err != nil {
				t.Fatalf("FetchArtifact failed: %v", err)
			}
			if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform()); diff != "" {
				t.Errorf("FetchArtifact after failed update mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// UpdateArtifact must read its row with a lock: while another transaction holds the row, the
// update waits, and once that transaction commits, the update merges into the committed state
// rather than into the state it would have read before.
func TestUpdateArtifact_LocksRowAgainstLostUpdates(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	createArtifact(ctx, t, repo, blankArtifactProto(123, 456))

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() unexpected error: %v", err)
	}
	defer holder.Rollback(ctx) //nolint:errcheck // Rolling back after commit is a harmless no-op.
	if _, err := holder.Exec(ctx, "SELECT 1 FROM Artifacts WHERE ArtifactId = $1 FOR NO KEY UPDATE", 456); err != nil {
		t.Fatalf("locking the artifact row unexpected error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
			AID:        456,
			Artifact:   &pb.Artifact{MimeType: "text/plain"},
			UpdateMask: &fmpb.FieldMask{Paths: []string{"mime_type"}},
		})
		done <- err
	}()

	waitForLockWaiter(ctx, t, pool)
	select {
	case err := <-done:
		t.Fatalf("UpdateArtifact() returned %v while the row was locked by another transaction", err)
	default:
	}

	// Change the row while the update is waiting, then release it.
	if _, err := holder.Exec(ctx, "UPDATE Artifacts SET Notes = 'holder-notes' WHERE ArtifactId = $1", 456); err != nil {
		t.Fatalf("updating the locked artifact row unexpected error: %v", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("Commit() unexpected error: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpdateArtifact() unexpected error: %v", err)
		}
	case <-time.After(lockWaitTimeout):
		t.Fatal("UpdateArtifact() did not complete after the lock was released")
	}

	gotArt, err := repo.FetchArtifact(ctx, 456)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if gotArt.GetNotes() != "holder-notes" || gotArt.GetMimeType() != "text/plain" {
		t.Errorf("FetchArtifact after concurrent updates = notes %q, mime type %q; want %q and %q (a lost update)", gotArt.GetNotes(), gotArt.GetMimeType(), "holder-notes", "text/plain")
	}
}

// Concurrent UpdateArtifact calls on the same artifact, each updating a different field, must all
// be reflected in the final row: every update merges into the state committed by the previous one.
func TestUpdateArtifact_ConcurrentUpdatesDoNotLoseFields(t *testing.T) {
	const rounds = 3
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	createArtifact(ctx, t, repo, blankArtifactProto(123, 456))

	// Each field update writes a value derived from the round, so that a stale write of a previous
	// round is detected as well.
	fieldUpdates := []struct {
		path  string
		apply func(art *pb.Artifact, round int)
	}{
		{"url", func(a *pb.Artifact, r int) {
			a.Url = &pb.Url{Url: fmt.Sprintf("http://round-%d.com", r), DisplayName: fmt.Sprintf("d%d", r)}
		}},
		{"tags", func(a *pb.Artifact, r int) { a.Tags = []string{fmt.Sprintf("tag-%d", r)} }},
		{"notes", func(a *pb.Artifact, r int) { a.Notes = fmt.Sprintf("notes-%d", r) }},
		{"lifecycle_phase", func(a *pb.Artifact, r int) { a.LifecyclePhase = pb.Artifact_LifecyclePhase(1 + r%3) }},
		{"data_type", func(a *pb.Artifact, r int) { a.DataType = pb.Artifact_DataType(1 + r) }},
		{"mime_type", func(a *pb.Artifact, r int) { a.MimeType = fmt.Sprintf("type/%d", r) }},
		{"additional_info", func(a *pb.Artifact, r int) {
			a.AdditionalInfo = &pb.Artifact_AdditionalInfo{Title: fmt.Sprintf("info-%d", r)}
		}},
		{"payload", func(a *pb.Artifact, r int) {
			a.Payload = &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{Title: fmt.Sprintf("t%d", r), Text: fmt.Sprintf("text-%d", r)}}}
		}},
	}

	for round := 1; round <= rounds; round++ {
		errs := make([]error, len(fieldUpdates))
		var wg sync.WaitGroup
		for i, fu := range fieldUpdates {
			wg.Go(func() {
				update := &pb.Artifact{}
				fu.apply(update, round)
				_, errs[i] = repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{
					AID:        456,
					Artifact:   update,
					UpdateMask: &fmpb.FieldMask{Paths: []string{fu.path}},
				})
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d: UpdateArtifact() unexpected error: %v", round, err)
		}

		want := blankArtifactProto(123, 456)
		for _, fu := range fieldUpdates {
			fu.apply(want, round)
		}
		gotArt, err := repo.FetchArtifact(ctx, 456)
		if err != nil {
			t.Fatalf("round %d: FetchArtifact failed: %v", round, err)
		}
		ignoreTimestamps := protocmp.IgnoreFields(&pb.Artifact{}, "create_time", "update_time")
		if diff := cmp.Diff(want, gotArt, protocmp.Transform(), ignoreTimestamps); diff != "" {
			t.Errorf("round %d: FetchArtifact after concurrent updates mismatch (-want +got); missing fields are lost updates:\n%s", round, diff)
		}
	}
}

// ---------------------------------------------------------------------------
// DeleteArtifact
// ---------------------------------------------------------------------------

func TestDeleteArtifact(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)

	art := &pb.Artifact{
		Name:   "artifacts/456",
		Parent: "experiments/123",
		Url: &pb.Url{
			Url: "http://artifact.com",
		},
		Tags: []string{"tag1", "tag2"},
	}
	createArtifact(ctx, t, repo, art)
	// A second artifact of the same experiment must survive the delete.
	otherArt := createArtifact(ctx, t, repo, blankArtifactProto(123, 457))

	// Verify it exists before deleting.
	if _, err := repo.FetchArtifact(ctx, 456); err != nil {
		t.Fatalf("FetchArtifact before delete failed: %v", err)
	}

	if err := repo.DeleteArtifact(ctx, 456); err != nil {
		t.Fatalf("DeleteArtifact failed: %v", err)
	}

	// Verify it is deleted.
	if _, err := repo.FetchArtifact(ctx, 456); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchArtifact for deleted AID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
	gotOther, err := repo.FetchArtifact(ctx, 457)
	if err != nil {
		t.Fatalf("FetchArtifact of the other artifact failed: %v", err)
	}
	if diff := cmp.Diff(otherArt, gotOther, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact of the other artifact mismatch (-want +got):\n%s", diff)
	}

	// Deleting again reports the artifact as gone.
	if err := repo.DeleteArtifact(ctx, 456); !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("DeleteArtifact of a deleted AID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestDeleteArtifact_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	err := repo.DeleteArtifact(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("DeleteArtifact for non-existent AID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

// Deleting an artifact deletes its ArtifactCharts rows, as the interleaved table does in Spanner,
// but not the Charts they reference.
func TestDeleteArtifact_CascadesArtifactCharts(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	insertExperiment(ctx, t, pool, 123)
	createArtifact(ctx, t, repo, blankArtifactProto(123, 456))
	createArtifact(ctx, t, repo, blankArtifactProto(123, 457))
	insertArtifactChart(ctx, t, pool, 123, 456, "chart-a")
	insertArtifactChart(ctx, t, pool, 123, 456, "chart-b")
	insertArtifactChart(ctx, t, pool, 123, 457, "chart-c")

	if err := repo.DeleteArtifact(ctx, 456); err != nil {
		t.Fatalf("DeleteArtifact failed: %v", err)
	}

	if n := countRows(ctx, t, pool, "SELECT count(*) FROM ArtifactCharts WHERE ArtifactId = 456"); n != 0 {
		t.Errorf("ArtifactCharts rows of the deleted artifact = %d, want 0", n)
	}
	if n := countRows(ctx, t, pool, "SELECT count(*) FROM ArtifactCharts WHERE ArtifactId = 457"); n != 1 {
		t.Errorf("ArtifactCharts rows of the other artifact = %d, want 1", n)
	}
	if n := countRows(ctx, t, pool, "SELECT count(*) FROM Charts"); n != 3 {
		t.Errorf("Charts rows after delete = %d, want 3", n)
	}
}
