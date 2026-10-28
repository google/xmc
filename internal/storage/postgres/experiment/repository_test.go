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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
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

	nilRepo := NewRepository(nil)
	if nilRepo.db != nil {
		t.Errorf("NewRepository(nil).db = %v, want nil", nilRepo.db)
	}
	if nilRepo.pool != nil {
		t.Errorf("NewRepository(nil).pool = %v, want nil", nilRepo.pool)
	}

	txRepo := repo.WithTx(pool)
	if txRepo.tx != pool {
		t.Errorf("WithTx(pool).tx = %v, want %p", txRepo.tx, pool)
	}
}

func TestLockExperimentSQL_UsesForNoKeyUpdate(t *testing.T) {
	sql := lockExperimentSQL.String()
	if !strings.HasSuffix(sql, "FOR NO KEY UPDATE") {
		t.Errorf("lockExperimentSQL = %q, want suffix FOR NO KEY UPDATE", sql)
	}
}

func TestRepository_NilPoolFailsPrecondition(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "FetchExperiment",
			call: func() error {
				_, err := repo.FetchExperiment(ctx, 1)
				return err
			},
		},
		{
			name: "FetchExperimentStatus",
			call: func() error {
				_, err := repo.FetchExperimentStatus(ctx, 1)
				return err
			},
		},
		{
			name: "ListExperiments",
			call: func() error {
				_, _, err := repo.ListExperiments(ctx, 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "UpdateExperiment",
			call: func() error {
				_, err := repo.UpdateExperiment(ctx, &storage.ExperimentUpdate{
					XID:        1,
					Experiment: &pb.Experiment{Title: "t"},
					UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
				})
				return err
			},
		},
		{
			name: "FetchDriftedComponents",
			call: func() error {
				_, err := repo.FetchDriftedComponents(ctx, 1, 10)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, grpcerr.ErrFailedPrecondition) || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s error = %v (code %v), want %v (code %v)", tt.name, err, status.Code(err), grpcerr.ErrFailedPrecondition, codes.FailedPrecondition)
			}
		})
	}
}

func TestUpdateExperiment_NilInputValidation(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(nil)

	if _, err := repo.UpdateExperiment(ctx, nil); err == nil {
		t.Errorf("UpdateExperiment(nil) = nil, want non-nil error")
	}

	_, err := repo.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        1,
		Experiment: &pb.Experiment{Title: "t"},
	})
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateExperiment(nil mask) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
}

func TestTxRepository_NilExperimentAndNilTransaction(t *testing.T) {
	ctx := context.Background()
	r := &TxRepository{}
	exp := &pb.Experiment{Name: "experiments/1001"}

	if _, err := r.FetchExperiment(ctx, 1001); err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("FetchExperiment(nil tx) error = %v, want 'transaction must not be nil'", err)
	}
	if err := r.InsertExperiment(ctx, nil); err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("InsertExperiment(nil exp) error = %v, want 'experiment must not be nil'", err)
	}
	if err := r.InsertExperiment(ctx, exp); err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("InsertExperiment(nil tx) error = %v, want 'transaction must not be nil'", err)
	}
	if err := r.UpdateExperimentStatus(ctx, nil); err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("UpdateExperimentStatus(nil exp) error = %v, want 'experiment must not be nil'", err)
	}
	if err := r.UpdateExperimentStatus(ctx, exp); err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("UpdateExperimentStatus(nil tx) error = %v, want 'transaction must not be nil'", err)
	}
	if err := r.UpdateExperimentResources(ctx, nil); err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("UpdateExperimentResources(nil exp) error = %v, want 'experiment must not be nil'", err)
	}
	if err := r.UpdateExperimentResources(ctx, exp); err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("UpdateExperimentResources(nil tx) error = %v, want 'transaction must not be nil'", err)
	}
}

func TestExtractExperimentCursor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	nowProto := timestamppb.New(now)

	testCases := []struct {
		name    string
		entry   *pb.Experiment
		want    map[string]any
		wantErr bool
	}{
		{
			name: "valid_experiment_entry",
			entry: &pb.Experiment{
				Name:         "experiments/123",
				CreateTime:   nowProto,
				UpdateTime:   nowProto,
				CompleteTime: nowProto,
			},
			want: map[string]any{
				"xid":       int64(123),
				"created":   now,
				"updated":   now,
				"completed": now,
			},
		},
		{
			name: "valid_experiment_entry_no_timestamps",
			entry: &pb.Experiment{
				Name: "experiments/456",
			},
			want: map[string]any{
				"xid":       int64(456),
				"completed": nil,
			},
		},
		{
			name: "invalid_experiment_name",
			entry: &pb.Experiment{
				Name: "invalid/789",
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractExperimentCursor(tc.entry)
			if (err != nil) != tc.wantErr {
				t.Errorf("ExtractExperimentCursor(%v) error = %v, wantErr = %t", tc.entry, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractExperimentCursor(%v) mismatch (-want +got):\n%s", tc.entry, diff)
			}
		})
	}
}
