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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "protobuf/field_mask_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

func TestUpdateExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	// 1. Create an Experiment.
	createdExp := spanner.CreateBlankExperiment(ctx, t, s, 123)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdExp.GetName(), err)
	}

	// 2. Update the Experiment.
	update := &storage.ExperimentUpdate{
		XID: xid,
		Experiment: &essmsgpb.Experiment{
			Title: "new-title",
			Notes: "new-notes",
			Tags:  []string{"tag3"},
			Urls:  []*essmsgpb.Url{{Url: "http://new.com"}},
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title", "notes", "tags", "urls"}},
	}
	res, err := s.UpdateExperiment(ctx, update)
	if err != nil {
		t.Fatalf("UpdateExperiment failed: %v", err)
	}
	originalExp, updatedExp := res.Original, res.Updated

	// 3. Verify the update.
	if diff := cmp.Diff(createdExp, originalExp, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls")); diff != "" {
		t.Errorf("UpdateExperiment returned unexpected original experiment: diff (-want +got):\n%s", diff)
	}

	wantExp := createdExp
	wantExp.Title = "new-title"
	wantExp.Notes = "new-notes"
	wantExp.Tags = []string{"tag3"}
	wantExp.Urls = []*essmsgpb.Url{{Url: "http://new.com"}}
	if diff := cmp.Diff(wantExp, updatedExp, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls", "update_time")); diff != "" {
		t.Errorf("UpdateExperiment returned unexpected experiment: diff (-want +got):\n%s", diff)
	}

	if !updatedExp.UpdateTime.AsTime().After(createdExp.UpdateTime.AsTime()) {
		t.Errorf("UpdateExperiment did not update UpdateTime: got %v, want > %v", updatedExp.UpdateTime, createdExp.UpdateTime)
	}
	if updatedExp.GetCreator() != createdExp.GetCreator() {
		t.Errorf("UpdateExperiment unexpectedly updated Creator: got %q, want %q", updatedExp.GetCreator(), createdExp.GetCreator())
	}

	// 4. Verify with FetchExperiment.
	gotExp, err := s.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if diff := cmp.Diff(updatedExp, gotExp, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls")); diff != "" {
		t.Errorf("FetchExperiment returned diff after update (-want +got):\n%s", diff)
	}
}

func TestUpdateExperiment_ImmutableField(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	createdExp := spanner.CreateBlankExperiment(ctx, t, s, 123)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdExp.GetName(), err)
	}

	update := &storage.ExperimentUpdate{
		XID: xid,
		Experiment: &essmsgpb.Experiment{
			Creator: "new-creator",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"creator"}},
	}
	_, err = s.UpdateExperiment(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateExperiment with immutable field returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}

func TestUpdateExperiment_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	update := &storage.ExperimentUpdate{
		XID: 123456789,
		Experiment: &essmsgpb.Experiment{
			Title: "new-title",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	}
	_, err := s.UpdateExperiment(ctx, update)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("UpdateExperiment for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestUpdateExperiment_EmptyMask(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	s, _ := spanner.NewStorage(essClient)

	createdExp := spanner.CreateBlankExperiment(ctx, t, s, 123)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdExp.GetName(), err)
	}

	update := &storage.ExperimentUpdate{
		XID:        xid,
		Experiment: &essmsgpb.Experiment{},
		UpdateMask: &fmpb.FieldMask{Paths: []string{}},
	}
	_, err = s.UpdateExperiment(ctx, update)
	if !errors.Is(err, status.ErrInvalidArgument) {
		t.Errorf("UpdateExperiment with empty mask returned error %v, want %v", err, status.ErrInvalidArgument)
	}
}
