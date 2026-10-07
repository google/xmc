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
	"errors"
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "protobuf/field_mask_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

func TestUpdateArtifact(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	// 1. Create an Experiment and Artifact.
	xid := int64(123)
	aid := int64(456)
	spanner.CreateBlankExperiment(ctx, t, s, xid)
	artifactProto := spanner.BlankArtifactProto(xid, aid)
	createdArt := spanner.CreateAndStoreArtifact(ctx, t, s, artifactProto)

	_, err := validator.ValidateArtifactName(createdArt.GetName())
	if err != nil {
		t.Fatalf("Could not parse AID from name %q: %v", createdArt.GetName(), err)
	}

	// 2. Update the Artifact.
	update := &storage.ArtifactUpdate{
		AID: aid,
		Artifact: &essmsgpb.Artifact{
			Notes: "new-notes",
			Tags:  []string{"tag3"},
			Url:   &essmsgpb.Url{Url: "http://new.com"},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes", "tags", "url"}},
	}
	res, err := s.UpdateArtifact(ctx, update)
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
	if diff := cmp.Diff(&essmsgpb.Url{Url: "http://new.com"}, updatedArt.GetUrl(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateArtifact returned artifact with wrong url: diff (-want +got):\n%s", diff)
	}
	if !updatedArt.UpdateTime.AsTime().After(createdArt.UpdateTime.AsTime()) {
		t.Errorf("UpdateArtifact did not update UpdateTime: got %v, want > %v", updatedArt.UpdateTime, createdArt.UpdateTime)
	}
	if updatedArt.GetParent() != createdArt.GetParent() {
		t.Errorf("UpdateArtifact unexpectedly updated Parent: got %q, want %q", updatedArt.GetParent(), createdArt.GetParent())
	}

	// 4. Verify with FetchArtifact.
	gotArt, err := s.FetchArtifact(ctx, aid)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}
	if diff := cmp.Diff(updatedArt, gotArt, protocmp.Transform()); diff != "" {
		t.Errorf("FetchArtifact returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateArtifact_ImmutableField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	xid := int64(123)
	aid := int64(456)
	spanner.CreateBlankExperiment(ctx, t, s, xid)
	artifactProto := spanner.BlankArtifactProto(xid, aid)
	spanner.CreateAndStoreArtifact(ctx, t, s, artifactProto)

	update := &storage.ArtifactUpdate{
		AID: aid,
		Artifact: &essmsgpb.Artifact{
			Name: "artifacts/789",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"name"}},
	}
	_, err := s.UpdateArtifact(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateArtifact with immutable field returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}

func TestUpdateArtifact_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	update := &storage.ArtifactUpdate{
		AID: 123456789,
		Artifact: &essmsgpb.Artifact{
			Notes: "new-notes",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"notes"}},
	}
	_, err := s.UpdateArtifact(ctx, update)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("UpdateArtifact for non-existent AID returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestUpdateArtifact_EmptyMask(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	xid := int64(123)
	aid := int64(456)
	spanner.CreateBlankExperiment(ctx, t, s, xid)
	artifactProto := spanner.BlankArtifactProto(xid, aid)
	spanner.CreateAndStoreArtifact(ctx, t, s, artifactProto)

	update := &storage.ArtifactUpdate{
		AID:        aid,
		Artifact:   &essmsgpb.Artifact{},
		UpdateMask: &fmpb.FieldMask{Paths: []string{}},
	}
	_, err := s.UpdateArtifact(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateArtifact with empty mask returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}
