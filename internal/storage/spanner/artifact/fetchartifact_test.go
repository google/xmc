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
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/validator/validator"
)

func TestFetchArtifact(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storage, 123)

	art := &essmsgpb.Artifact{
		Name:   "artifacts/456",
		Parent: "experiments/123",
		Url: &essmsgpb.Url{
			Url: "http://artifact.com",
		},
		Tags: []string{"tag1", "tag2"},
	}
	createdArt, err := storage.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact failed: %v", err)
	}

	aid, err := validator.ValidateArtifactName(createdArt.GetName())
	if err != nil {
		t.Fatalf("Could not parse AID from name %q: %v", createdArt.GetName(), err)
	}

	gotArt, err := storage.FetchArtifact(ctx, aid)
	if err != nil {
		t.Fatalf("FetchArtifact failed: %v", err)
	}

	if diff := cmp.Diff(createdArt, gotArt, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Artifact{}, "acls")); diff != "" {
		t.Errorf("FetchArtifact(%d) returned diff (-want +got):\n%s", aid, diff)
	}
}

func TestFetchArtifact_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	_, err = storage.FetchArtifact(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchArtifact for non-existent AID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}
