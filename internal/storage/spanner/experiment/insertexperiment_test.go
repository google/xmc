// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment_test

import (
	"context"
	"testing"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestInsertExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	now := timestamppb.New(time.Now())
	exp := &essmsgpb.Experiment{
		Name:       "experiments/789",
		Title:      "insert-title",
		Creator:    "test-creator",
		Urls:       []*essmsgpb.Url{},
		Tags:       []string{},
		Resources:  []*essmsgpb.ResourceSet{},
		CreateTime: now,
		UpdateTime: now,
		Status: &essmsgpb.ExperimentStatus{
			ActiveState:               essmsgpb.ExperimentStatus_ACTIVE,
			UpdateTime:                now,
			LastChildStatusUpdateTime: now,
		},
	}

	err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Experiments.InsertExperiment(ctx, exp)
	})
	if err != nil {
		t.Fatalf("InsertExperiment failed: %v", err)
	}

	fetched, err := storageClient.FetchExperiment(ctx, 789)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if fetched.GetName() != exp.GetName() {
		t.Errorf("FetchExperiment got name %q, want %q", fetched.GetName(), exp.GetName())
	}
}

func TestInsertExperiment_Nil(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Experiments.InsertExperiment(ctx, nil)
	})
	if err == nil {
		t.Errorf("InsertExperiment(nil) succeeded, want error")
	}
}
