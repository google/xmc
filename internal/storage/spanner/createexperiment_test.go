// Copyright 2025 Google LLC
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
package spanner

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestCreateExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:    "experiments/123",
		Title:   "test-title",
		Creator: "test-creator",
		Notes:   "test-notes",
		Tags:    []string{"tag1", "tag2"},
		Urls: []*essmsgpb.Url{
			{
				Url: "http://test.com",
			},
		},
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}

	createdExp, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	if createdExp.GetName() != exp.GetName() {
		t.Errorf("CreateExperiment returned experiment with wrong name: got %q, want %q", createdExp.GetName(), exp.GetName())
	}
	if createdExp.GetCreateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil CreateTime")
	}
	if createdExp.GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil UpdateTime")
	}
	if createdExp.GetStatus().GetLastChildStatusUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil LastChildStatusUpdateTime in Status")
	}
	if createdExp.GetStatus().GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil UpdateTime in Status")
	}
	if createdExp.GetCreator() != exp.GetCreator() {
		t.Errorf("CreateExperiment returned experiment with wrong creator: got %q, want %q", createdExp.GetCreator(), exp.GetCreator())
	}
}

func TestCreateExperiment_InvalidName(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name: "invalid-name",
	}

	_, err := storage.CreateExperiment(ctx, exp)
	if err == nil {
		t.Errorf("CreateExperiment with invalid name succeeded, want error")
	}
}
