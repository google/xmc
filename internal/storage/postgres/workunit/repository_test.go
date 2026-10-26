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
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}
	if repo.pool != pool {
		t.Errorf("NewRepository(pool).pool = %v, want %p", repo.pool, pool)
	}

	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestRepository_NilPoolAndValidation(t *testing.T) {
	repo := NewRepository(nil)
	txRepo := repo.WithTx(nil)
	ctx := context.Background()

	tests := []struct {
		name      string
		call      func() error
		wantCode  codes.Code
		wantIsErr error
	}{
		{
			name: "FetchWorkUnit nil pool",
			call: func() error {
				_, err := repo.FetchWorkUnit(ctx, 1, 1)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "FetchCurrentWorkUnitExecution nil pool",
			call: func() error {
				_, err := repo.FetchCurrentWorkUnitExecution(ctx, 1, 1)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "ListWorkUnitExecutions nil pool",
			call: func() error {
				_, err := repo.ListWorkUnitExecutions(ctx, 1, 1)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "UpdateWorkUnit nil update",
			call: func() error {
				_, err := repo.UpdateWorkUnit(ctx, nil)
				return err
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "UpdateWorkUnit nil pool",
			call: func() error {
				_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{XID: 1, WID: 1})
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "ListWorkUnits nil pool",
			call: func() error {
				_, _, err := repo.ListWorkUnits(ctx, "experiments/1", 10, "", "", "", 0)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "FetchActiveWorkUnitsByQueues nil pool",
			call: func() error {
				_, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q1"}, 10, "")
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_FetchWorkUnit nil tx",
			call: func() error {
				_, err := txRepo.FetchWorkUnit(ctx, 1, 1)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_FetchUpdatingWorkUnits nil tx",
			call: func() error {
				_, err := txRepo.FetchUpdatingWorkUnits(ctx, 1, []int64{1})
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_SaveWorkUnitExecution nil execution",
			call: func() error {
				return txRepo.SaveWorkUnitExecution(ctx, nil)
			},
			wantIsErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "Tx_SaveWorkUnitExecution nil tx",
			call: func() error {
				return txRepo.SaveWorkUnitExecution(ctx, &essmsgpb.WorkUnitExecution{Name: "experiments/1/workUnits/1/executions/1"})
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_UpdateCurrentWorkUnitExecution nil execution",
			call: func() error {
				return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, nil)
			},
			wantIsErr: grpcerr.ErrInvalidArgument,
		},
		{
			name: "Tx_UpdateCurrentWorkUnitExecution nil tx",
			call: func() error {
				return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, &essmsgpb.WorkUnitExecution{})
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_ListWorkUnitMessagesBySeverity nil tx",
			call: func() error {
				_, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, 1, essmsgpb.StructuredMessage_ERROR)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_FetchNextWorkUnitID nil tx",
			call: func() error {
				_, err := txRepo.FetchNextWorkUnitID(ctx, 1)
				return err
			},
			wantCode: codes.FailedPrecondition,
		},
		{
			name: "Tx_InsertWorkUnit nil work unit",
			call: func() error {
				return txRepo.InsertWorkUnit(ctx, nil)
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "Tx_InsertWorkUnit nil tx",
			call: func() error {
				return txRepo.InsertWorkUnit(ctx, &essmsgpb.WorkUnit{})
			},
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatalf("%s returned nil error", tt.name)
			}
			if tt.wantIsErr != nil {
				if !errors.Is(err, tt.wantIsErr) {
					t.Errorf("%s error = %v, want errors.Is(_, %v)", tt.name, err, tt.wantIsErr)
				}
				return
			}
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, tt.wantCode, err)
			}
		})
	}
}

func TestFetchActiveWorkUnitsByQueues_ContextAndTokenErrors(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		repo := NewRepository(nil)
		_, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q1"}, 10, "")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("FetchActiveWorkUnitsByQueues() error = %v, want %v", err, context.Canceled)
		}
	})

	t.Run("empty queueNames", func(t *testing.T) {
		repo := NewRepository(&pgxpool.Pool{})
		res, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), nil, 10, "")
		if err != nil {
			t.Fatalf("FetchActiveWorkUnitsByQueues() unexpected error = %v", err)
		}
		if len(res.WorkUnits) != 0 || res.NextPageToken != "" || res.TotalQueueSize != 0 {
			t.Errorf("FetchActiveWorkUnitsByQueues() = %+v, want empty result", res)
		}
	})

	invalidTokens := []struct {
		name  string
		token string
	}{
		{name: "invalid base64", token: "!!!invalid_base64!!!"},
		{name: "not an integer", token: base64.URLEncoding.EncodeToString([]byte("abc"))},
		{name: "negative integer", token: base64.URLEncoding.EncodeToString([]byte("-5"))},
	}
	for _, tt := range invalidTokens {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewRepository(&pgxpool.Pool{})
			_, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), []string{"q1"}, 10, tt.token)
			if !errors.Is(err, storage.ErrInvalidPageToken) {
				t.Errorf("FetchActiveWorkUnitsByQueues() error = %v, want %v", err, storage.ErrInvalidPageToken)
			}
		})
	}
}

func TestExtractWorkUnitCursor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	later := now.Add(time.Hour)
	evenLater := now.Add(2 * time.Hour)

	tests := []struct {
		name    string
		wu      *essmsgpb.WorkUnit
		want    map[string]any
		wantErr bool
	}{
		{
			name: "valid work unit with all fields",
			wu: &essmsgpb.WorkUnit{
				Name:       "experiments/123/workUnits/456",
				CreateTime: timestamppb.New(now),
				UpdateTime: timestamppb.New(later),
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_RUNNING,
					},
					CompleteTime: timestamppb.New(evenLater),
				},
			},
			want: map[string]any{
				"xid":                 int64(123),
				"work_unit_id":        int64(456),
				"state":               "RUNNING",
				"created":             now,
				"updated":             later,
				"execution_completed": evenLater,
			},
		},
		{
			name: "valid work unit with minimal fields",
			wu: &essmsgpb.WorkUnit{
				Name: "experiments/123/workUnits/456",
			},
			want: map[string]any{
				"xid":                 int64(123),
				"work_unit_id":        int64(456),
				"state":               "WORK_UNIT_STATE_UNSPECIFIED",
				"execution_completed": nil,
			},
		},
		{
			name: "invalid work unit name",
			wu: &essmsgpb.WorkUnit{
				Name: "invalid-name",
			},
			wantErr: true,
		},
		{
			name:    "nil work unit",
			wu:      nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractWorkUnitCursor(tc.wu)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ExtractWorkUnitCursor() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractWorkUnitCursor() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
