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

package statusmessage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

const (
	testXID int64 = 123
	testWID int64 = 456
)

func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return NewRepository(essPool), essPool
}

func insertStatusMessage(ctx context.Context, t *testing.T, repo *Repository, pool *pgxpool.Pool, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) {
	t.Helper()
	err := pgdb.RunInTx(ctx, pool, func(txCtx context.Context, tx pgx.Tx) error {
		return repo.WithTx(tx).SaveStatusMessageHistory(txCtx, eid, oldMsg, newMsg)
	})
	if err != nil {
		t.Fatalf("SaveStatusMessageHistory() failed: %v", err)
	}
}

func messageTexts(msgs []*essmsgpb.StatusMessage) []string {
	texts := make([]string, len(msgs))
	for i, m := range msgs {
		texts[i] = m.GetContent().GetUserFacingText()
	}
	return texts
}

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestSaveStatusMessageHistory_SuccessAndNullRoundTrips(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	baseTime := time.Date(2026, time.April, 13, 10, 0, 0, 0, time.UTC)

	t.Run("experiment message with NULL optional columns", func(t *testing.T) {
		expMsg := &essmsgpb.StructuredMessage{
			Parent:         "experiments/123",
			Severity:       essmsgpb.StructuredMessage_INFO,
			Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
			UserFacingText: "experiment-level status",
			CreateTime:     timestamppb.New(baseTime),
		}
		insertStatusMessage(ctx, t, repo, pool, nil, nil, expMsg)

		const nullCheckSQL = `SELECT WorkUnitId IS NULL, ExecutionId IS NULL, UserFacingUrl IS NULL, PreviousMessage IS NULL, AuxiliaryInfo IS NULL
FROM StatusMessages WHERE Xid = $1 AND UserFacingText = $2`
		var widNull, eidNull, urlNull, prevNull, auxNull bool
		if err := pool.QueryRow(ctx, nullCheckSQL, testXID, expMsg.UserFacingText).Scan(&widNull, &eidNull, &urlNull, &prevNull, &auxNull); err != nil {
			t.Fatalf("QueryRow(nullCheckSQL) failed: %v", err)
		}
		if !widNull || !eidNull || !urlNull || !prevNull || !auxNull {
			t.Errorf("NULL columns = (wid=%v, eid=%v, url=%v, prev=%v, aux=%v), want all true", widNull, eidNull, urlNull, prevNull, auxNull)
		}

		msgs, token, err := repo.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages() failed: %v", err)
		}
		if token != "" || len(msgs) != 1 {
			t.Fatalf("ListStatusMessages() = (%d msgs, token %q), want (1, \"\")", len(msgs), token)
		}
		if !strings.HasPrefix(msgs[0].GetName(), "experiments/123/statusMessages/") {
			t.Errorf("msgs[0].Name = %q, want prefix \"experiments/123/statusMessages/\"", msgs[0].GetName())
		}
		if msgs[0].GetExecutionId() != 0 {
			t.Errorf("msgs[0].ExecutionId = %d, want 0", msgs[0].GetExecutionId())
		}
		if diff := cmp.Diff(expMsg, msgs[0].GetContent(), protocmp.Transform()); diff != "" {
			t.Errorf("msgs[0].Content mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("work unit message with all optional fields and PreviousMessage", func(t *testing.T) {
		eid := int64(7)
		oldMsg := &essmsgpb.StructuredMessage{
			Parent:         "experiments/123/workUnits/456",
			Severity:       essmsgpb.StructuredMessage_WARNING,
			Source:         essmsgpb.StructuredMessage_SCHEDULER,
			UserFacingText: "previous message",
			CreateTime:     timestamppb.New(baseTime),
		}
		newMsg := &essmsgpb.StructuredMessage{
			Parent:         "experiments/123/workUnits/456",
			Severity:       essmsgpb.StructuredMessage_ERROR,
			Source:         essmsgpb.StructuredMessage_ADAPTER,
			UserFacingText: "updated work unit status",
			UserFacingUrl:  &essmsgpb.Url{Url: "https://logs.example.com", DisplayName: "Logs"},
			CreateTime:     timestamppb.New(baseTime.Add(time.Minute)),
			AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
				Detail: &essmsgpb.AuxiliaryInfo_StackTrace{StackTrace: "panic: boom"},
			},
		}
		insertStatusMessage(ctx, t, repo, pool, &eid, oldMsg, newMsg)

		var prevBytes []byte
		if err := pool.QueryRow(ctx, `SELECT PreviousMessage FROM StatusMessages WHERE Xid = $1 AND WorkUnitId = $2`, testXID, testWID).Scan(&prevBytes); err != nil {
			t.Fatalf("QueryRow(PreviousMessage) failed: %v", err)
		}
		gotPrev, err := postgresdata.UnmarshalProto[essmsgpb.StructuredMessage](prevBytes)
		if err != nil {
			t.Fatalf("UnmarshalProto(PreviousMessage) failed: %v", err)
		}
		if diff := cmp.Diff(oldMsg, gotPrev, protocmp.Transform()); diff != "" {
			t.Errorf("PreviousMessage mismatch (-want +got):\n%s", diff)
		}

		msgs, _, err := repo.ListStatusMessages(ctx, "experiments/123/workUnits/456", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages(workUnit) failed: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("ListStatusMessages(workUnit) returned %d messages, want 1", len(msgs))
		}
		if !strings.HasPrefix(msgs[0].GetName(), "experiments/123/workUnits/456/statusMessages/") {
			t.Errorf("msgs[0].Name = %q, want prefix \"experiments/123/workUnits/456/statusMessages/\"", msgs[0].GetName())
		}
		if msgs[0].GetExecutionId() != eid {
			t.Errorf("msgs[0].ExecutionId = %d, want %d", msgs[0].GetExecutionId(), eid)
		}
		if diff := cmp.Diff(newMsg, msgs[0].GetContent(), protocmp.Transform()); diff != "" {
			t.Errorf("msgs[0].Content mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestTxRepository_SaveStatusMessageHistory_ValidationAndErrors(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	validMsg := &essmsgpb.StructuredMessage{Parent: "experiments/123", UserFacingText: "ok"}
	invalidParentMsg := &essmsgpb.StructuredMessage{Parent: "invalid-parent", UserFacingText: "bad"}

	tests := []struct {
		name     string
		call     func() error
		wantCode codes.Code
		wantErr  error
	}{
		{
			name: "nil context returns InvalidArgument",
			call: func() error {
				//nolint:staticcheck // Intentionally testing nil context guard.
				return repo.WithTx(pool).SaveStatusMessageHistory(nil, nil, nil, validMsg)
			},
			wantCode: codes.InvalidArgument,
			wantErr:  grpcerr.ErrInvalidArgument,
		},
		{
			name: "canceled context returns context.Canceled",
			call: func() error {
				return repo.WithTx(pool).SaveStatusMessageHistory(canceledCtx, nil, nil, validMsg)
			},
			wantCode: codes.Unknown,
			wantErr:  context.Canceled,
		},
		{
			name: "nil transaction returns FailedPrecondition",
			call: func() error {
				return repo.WithTx(nil).SaveStatusMessageHistory(ctx, nil, nil, validMsg)
			},
			wantCode: codes.FailedPrecondition,
			wantErr:  grpcerr.ErrFailedPrecondition,
		},
		{
			name: "invalid parent returns InvalidArgument",
			call: func() error {
				return pgdb.RunInTx(ctx, pool, func(txCtx context.Context, tx pgx.Tx) error {
					return repo.WithTx(tx).SaveStatusMessageHistory(txCtx, nil, nil, invalidParentMsg)
				})
			},
			wantCode: codes.InvalidArgument,
			wantErr:  grpcerr.ErrInvalidArgument,
		},
		{
			name: "missing foreign-key experiment returns FailedPrecondition",
			call: func() error {
				return pgdb.RunInTx(ctx, pool, func(txCtx context.Context, tx pgx.Tx) error {
					return repo.WithTx(tx).SaveStatusMessageHistory(txCtx, nil, nil, validMsg)
				})
			},
			wantCode: codes.FailedPrecondition,
			wantErr:  grpcerr.ErrFailedPrecondition,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.wantErr) || status.Code(err) != tc.wantCode {
				t.Errorf("SaveStatusMessageHistory() = %v (code %v), want %v (code %v)", err, status.Code(err), tc.wantErr, tc.wantCode)
			}
		})
	}
}

func TestListStatusMessages(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Test experiment message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
	})
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Test work unit message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123/workUnits/456",
	})

	gotExpMessages, gotPageToken, err := repo.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages(experiment) failed: %v", err)
	}
	if len(gotExpMessages) != 2 || gotPageToken != "" {
		t.Errorf("ListStatusMessages(experiment) = (%d msgs, token %q), want (2, \"\")", len(gotExpMessages), gotPageToken)
	}

	gotWUMessages, _, err := repo.ListStatusMessages(ctx, "experiments/123/workUnits/456", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages(workUnit) failed: %v", err)
	}
	if diff := cmp.Diff([]string{"Test work unit message"}, messageTexts(gotWUMessages)); diff != "" {
		t.Errorf("ListStatusMessages(workUnit) texts mismatch (-want +got):\n%s", diff)
	}
}

func TestListStatusMessages_Filtering(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message One",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
	})
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Message Two",
		Severity:       essmsgpb.StructuredMessage_ERROR,
		Source:         essmsgpb.StructuredMessage_ADAPTER,
		Parent:         "experiments/123",
	})

	tests := []struct {
		name      string
		filter    string
		wantTexts []string
	}{
		{
			name:      "filter by source enum string",
			filter:    `source = "XMANAGER_BACKEND"`,
			wantTexts: []string{"Message One"},
		},
		{
			name:      "filter by severity enum identifier",
			filter:    `severity = ERROR`,
			wantTexts: []string{"Message Two"},
		},
		{
			name:      "filter by user_facing_text",
			filter:    `user_facing_text = "Message One"`,
			wantTexts: []string{"Message One"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs, _, err := repo.ListStatusMessages(ctx, "experiments/123", 10, "", tc.filter, "", 0)
			if err != nil {
				t.Fatalf("ListStatusMessages(%q) failed: %v", tc.filter, err)
			}
			if diff := cmp.Diff(tc.wantTexts, messageTexts(msgs)); diff != "" {
				t.Errorf("ListStatusMessages(%q) texts mismatch (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListStatusMessages_OrderingAndPagination(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	baseTime := time.Date(2026, time.April, 13, 0, 0, 0, 0, time.UTC)
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Oldest Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(baseTime),
	})
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Middle Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123/workUnits/1",
		CreateTime:     timestamppb.New(baseTime.Add(time.Hour)),
	})
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "Newest Message",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(baseTime.Add(2 * time.Hour)),
	})

	t.Run("default ordering is create_time desc across experiment and work unit messages", func(t *testing.T) {
		msgs, _, err := repo.ListStatusMessages(ctx, "experiments/123", 10, "", "", "", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages() failed: %v", err)
		}
		want := []string{"Newest Message", "Middle Message", "Oldest Message"}
		if diff := cmp.Diff(want, messageTexts(msgs)); diff != "" {
			t.Errorf("default ordering mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("explicit create_time asc ordering", func(t *testing.T) {
		msgs, _, err := repo.ListStatusMessages(ctx, "experiments/123", 10, "", "", "create_time asc", 0)
		if err != nil {
			t.Fatalf("ListStatusMessages(create_time asc) failed: %v", err)
		}
		want := []string{"Oldest Message", "Middle Message", "Newest Message"}
		if diff := cmp.Diff(want, messageTexts(msgs)); diff != "" {
			t.Errorf("ascending ordering mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("multi-page pagination preserves default newest-first order", func(t *testing.T) {
		var collected []string
		pageToken := ""
		for page := 1; page <= 3; page++ {
			msgs, nextToken, err := repo.ListStatusMessages(ctx, "experiments/123", 1, pageToken, "", "", 0)
			if err != nil {
				t.Fatalf("ListStatusMessages(page %d) failed: %v", page, err)
			}
			if len(msgs) != 1 {
				t.Fatalf("page %d returned %d messages, want 1", page, len(msgs))
			}
			collected = append(collected, msgs[0].GetContent().GetUserFacingText())
			if page < 3 && nextToken == "" {
				t.Fatalf("page %d returned empty nextPageToken", page)
			}
			if page == 3 && nextToken != "" {
				t.Errorf("final page returned non-empty nextPageToken %q", nextToken)
			}
			pageToken = nextToken
		}
		want := []string{"Newest Message", "Middle Message", "Oldest Message"}
		if diff := cmp.Diff(want, collected); diff != "" {
			t.Errorf("paginated messages mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestListStatusMessages_SameCreateTimePagination(t *testing.T) {
	ctx := t.Context()
	repo, pool := newTestRepository(t)
	postgrestest.SeedExperiment(ctx, t, pool, testXID)

	sameTime := time.Date(2026, time.April, 13, 12, 0, 0, 0, time.UTC)
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "SameTime One",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(sameTime),
	})
	insertStatusMessage(ctx, t, repo, pool, nil, nil, &essmsgpb.StructuredMessage{
		UserFacingText: "SameTime Two",
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		Parent:         "experiments/123",
		CreateTime:     timestamppb.New(sameTime),
	})

	page1, token, err := repo.ListStatusMessages(ctx, "experiments/123", 1, "", "", "", 0)
	if err != nil || len(page1) != 1 || token == "" {
		t.Fatalf("ListStatusMessages(page 1) = (%d msgs, token %q, err %v), want (1, non-empty, nil)", len(page1), token, err)
	}
	page2, token2, err := repo.ListStatusMessages(ctx, "experiments/123", 1, token, "", "", 0)
	if err != nil || len(page2) != 1 || token2 != "" {
		t.Fatalf("ListStatusMessages(page 2) = (%d msgs, token %q, err %v), want (1, \"\", nil)", len(page2), token2, err)
	}
	if page1[0].GetName() == page2[0].GetName() {
		t.Errorf("page 1 and page 2 returned identical message %q", page1[0].GetName())
	}
}

func TestListStatusMessages_ValidationErrors(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	tests := []struct {
		name      string
		repo      *Repository
		parent    string
		pageToken string
		filter    string
		orderBy   string
		wantCode  codes.Code
	}{
		{
			name:     "nil db returns FailedPrecondition",
			repo:     NewRepository(nil),
			parent:   "experiments/123",
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "empty parent returns InvalidArgument",
			repo:     repo,
			parent:   "",
			wantCode: codes.InvalidArgument,
		},
		{
			name:      "invalid pageToken returns InvalidArgument",
			repo:      repo,
			parent:    "experiments/123",
			pageToken: "not-valid-base64!!!",
			wantCode:  codes.InvalidArgument,
		},
		{
			name:     "invalid filter returns InvalidArgument",
			repo:     repo,
			parent:   "experiments/123",
			filter:   "unknown_field = 1",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid orderBy returns InvalidArgument",
			repo:     repo,
			parent:   "experiments/123",
			orderBy:  "unknown_field asc",
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.repo.ListStatusMessages(ctx, tc.parent, 10, tc.pageToken, tc.filter, tc.orderBy, 0)
			if status.Code(err) != tc.wantCode {
				t.Errorf("ListStatusMessages() code = %v, want %v (err: %v)", status.Code(err), tc.wantCode, err)
			}
		})
	}
}

func TestExtractStatusMessageCursor(t *testing.T) {
	testTime := time.Date(2026, time.April, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		msg     *essmsgpb.StatusMessage
		wantWID any
		wantErr bool
	}{
		{
			name: "experiment status message has nil work_unit_id",
			msg: &essmsgpb.StatusMessage{
				Name: "experiments/123/statusMessages/10",
				Content: &essmsgpb.StructuredMessage{
					Parent:     "experiments/123",
					CreateTime: timestamppb.New(testTime),
				},
			},
			wantWID: nil,
		},
		{
			name: "work unit status message extracts work_unit_id from name",
			msg: &essmsgpb.StatusMessage{
				Name: "experiments/123/workUnits/456/statusMessages/10",
				Content: &essmsgpb.StructuredMessage{
					Parent:     "experiments/123/workUnits/456",
					CreateTime: timestamppb.New(testTime),
				},
			},
			wantWID: int64(456),
		},
		{
			name: "work unit status message falls back to parent when name omits workUnits segment",
			msg: &essmsgpb.StatusMessage{
				Name: "experiments/123/statusMessages/10",
				Content: &essmsgpb.StructuredMessage{
					Parent:     "experiments/123/workUnits/789",
					CreateTime: timestamppb.New(testTime),
				},
			},
			wantWID: int64(789),
		},
		{
			name: "invalid status message name returns error",
			msg: &essmsgpb.StatusMessage{
				Name: "invalid-name",
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cursor, err := ExtractStatusMessageCursor(tc.msg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ExtractStatusMessageCursor() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if _, hasBitReversed := cursor["bit_reversed_xid"]; hasBitReversed {
				t.Errorf("cursor unexpectedly contains bit_reversed_xid: %v", cursor)
			}
			if gotXID := cursor["xid"]; gotXID != int64(123) {
				t.Errorf("cursor[\"xid\"] = %v (%T), want int64(123)", gotXID, gotXID)
			}
			if gotWID := cursor["work_unit_id"]; gotWID != tc.wantWID {
				t.Errorf("cursor[\"work_unit_id\"] = %v (%T), want %v (%T)", gotWID, gotWID, tc.wantWID, tc.wantWID)
			}
		})
	}
}
