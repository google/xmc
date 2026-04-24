// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package spanner

import (
	"context"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// CreateArtifact creates a new artifact in Spanner. It returns the artifact proto with
// the timestamps set to the initial commit timestamps.
func (s *Storage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	// 1. Validate the experiment name and extract the XID.
	_, err := validator.ValidateArtifactName(artifact.Name)
	if err != nil {
		return nil, err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		// 2. Set the timestamps to the current time.
		now := timestamppb.New(time.Now())
		artifact.CreateTime = now
		artifact.UpdateTime = now

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
