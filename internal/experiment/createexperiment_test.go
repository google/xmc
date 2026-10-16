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
package experiment_test

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestCreateExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

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

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
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
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name: "invalid-name",
	}

	_, err := experiment.CreateExperiment(ctx, storage, exp)
	if err == nil {
		t.Errorf("CreateExperiment with invalid name succeeded, want error")
	}
}

func TestCreateExperiment_NilExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	_, err := experiment.CreateExperiment(ctx, storage, nil)
	if err == nil {
		t.Errorf("CreateExperiment(nil) succeeded, want error")
	}
}

func TestCreateExperiment_InitializesStatusIfNil(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/456",
		Title:     "test-title",
		Creator:   "test-creator",
		Tags:      []string{},
		Urls:      []*essmsgpb.Url{},
		Resources: []*essmsgpb.ResourceSet{},
		Status:    nil,
	}

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}
	if createdExp.GetStatus() == nil {
		t.Fatalf("CreateExperiment did not initialize nil status")
	}
	if createdExp.GetStatus().GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil Status.UpdateTime")
	}
}

func TestCreateExperiment_WithStructuredStatusMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/789",
		Title:     "test-title",
		Creator:   "test-creator",
		Tags:      []string{},
		Urls:      []*essmsgpb.Url{},
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			StructuredStatusMessage: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_INFO,
				UserFacingText: "initial status message",
				Parent:         "experiments/789",
			},
		},
	}

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	if got := createdExp.GetStatus().GetStructuredStatusMessage().GetUserFacingText(); got != "initial status message" {
		t.Errorf("CreateExperiment returned message %q, want %q", got, "initial status message")
	}

	messages, _, err := storage.ListStatusMessages(ctx, "experiments/789", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ListStatusMessages returned %d messages, want 1", len(messages))
	}
	if got := messages[0].GetContent().GetUserFacingText(); got != "initial status message" {
		t.Errorf("ListStatusMessages returned message text %q, want %q", got, "initial status message")
	}
}
