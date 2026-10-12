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
package artifact_test

import (
	"context"
	"github.com/google/xmc/internal/storage/spanner"
	"testing"

	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/storage/spanner/emulator"
)

func TestCreateArtifact(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

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

	_, err = storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	art := &essmsgpb.Artifact{
		Name:   "artifacts/456",
		Parent: "experiments/123",
		Url: &essmsgpb.Url{
			Url: "http://artifact.com",
		},
		Tags:  []string{"tag3", "tag4"},
		Notes: "artifact-notes",
	}

	createdArt, err := storage.CreateArtifact(ctx, art)
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
	if createdArt.GetParent() != art.GetParent() {
		t.Errorf("CreateArtifact returned artifact with wrong parent: got %q, want %q", createdArt.GetParent(), art.GetParent())
	}
}

func TestCreateArtifact_InvalidName(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	art := &essmsgpb.Artifact{
		Name: "invalid-name",
	}

	_, err = storage.CreateArtifact(ctx, art)
	if err == nil {
		t.Errorf("CreateArtifact with invalid name succeeded, want error")
	}
}
