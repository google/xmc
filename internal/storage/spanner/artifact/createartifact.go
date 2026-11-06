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

package artifact

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/validator"
)

// CreateArtifact creates a new artifact in Spanner. It returns the artifact proto with
// the timestamps set to the initial commit timestamps.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	// 1. Validate the experiment name and extract the XID.
	_, err := validator.ValidateArtifactName(artifact.Name)
	if err != nil {
		return nil, err
	}

	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		// 2. Set the timestamps to the current time.
		now := timestamppb.New(time.Now())
		artifact.CreateTime = now
		artifact.UpdateTime = now

		// TODO: Consider moving ArtifactProtoToInsertMutation from spannerdata package into the artifact repository package.
		// 3. Generate the insert mutation for the artifact.
		artifactMutation, err := spannerdata.ArtifactProtoToInsertMutation(artifact)
		if err != nil {
			logging.Error(ctx, "Failed to create mutation for artifact", "artifact", artifact, "error", err)
			return err
		}

		// 4. Buffer the write mutation.
		err = tx.BufferWrite([]*spanner.Mutation{artifactMutation})
		if err != nil {
			logging.Error(ctx, "Failed to buffer write mutation for create artifact", "error", err)
			return err
		}

		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to create artifact", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written artifact to storage")

	return artifact, nil
}
