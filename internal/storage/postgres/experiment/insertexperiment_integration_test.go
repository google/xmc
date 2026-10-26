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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
)

func TestInsertExperiment(t *testing.T) {
	ctx := t.Context()
	repo, essPool := newTestRepository(t)

	now := timestamppb.New(time.Now().Truncate(time.Microsecond))
	exp := &pb.Experiment{
		Name:       "experiments/789",
		Title:      "insert-title",
		Creator:    "test-creator",
		Urls:       []*pb.Url{},
		Tags:       []string{},
		Resources:  []*pb.ResourceSet{},
		CreateTime: now,
		UpdateTime: now,
		Status: &pb.ExperimentStatus{
			ActiveState:               pb.ExperimentStatus_ACTIVE,
			LaunchState:               pb.ExperimentStatus_LAUNCHING,
			UpdateTime:                now,
			LastChildStatusUpdateTime: now,
		},
	}

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.InsertExperiment(ctx, exp)
	})
	if err != nil {
		t.Fatalf("InsertExperiment() unexpected error: %v", err)
	}

	fetched, err := repo.FetchExperiment(ctx, 789)
	if err != nil {
		t.Fatalf("FetchExperiment(789) unexpected error: %v", err)
	}
	if diff := cmp.Diff(exp, fetched, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment(789) mismatch (-want +got):\n%s", diff)
	}

	wantDerived := derivedStateColumns{
		ActiveState: pb.ExperimentStatus_ACTIVE,
		LaunchState: pb.ExperimentStatus_LAUNCHING,
	}
	if diff := cmp.Diff(wantDerived, queryDerivedStateColumns(ctx, t, essPool, 789)); diff != "" {
		t.Errorf("stored derived state columns mismatch (-want +got):\n%s", diff)
	}
}

func TestInsertExperiment_DuplicateReturnsAlreadyExists(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exp := createBlankExperiment(ctx, t, repo, 789)

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.InsertExperiment(ctx, exp)
	})
	if !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
		t.Errorf("InsertExperiment(duplicate) error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrAlreadyExists, codes.AlreadyExists)
	}
}

func TestInsertExperiment_Nil(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.InsertExperiment(ctx, nil)
	})
	if err == nil {
		t.Errorf("InsertExperiment(nil) = nil, want non-nil error")
	}
}

func TestInsertExperiment_InvalidName(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	exp := blankExperimentProto(1)
	exp.Name = "invalid/1"

	err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
		return tx.InsertExperiment(ctx, exp)
	})
	if err == nil {
		t.Errorf("InsertExperiment(invalid name) = nil, want non-nil error")
	}
}
