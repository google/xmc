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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// CreateArtifact creates a new artifact in Spanner.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	_, err := validator.ValidateArtifactName(artifact.Name)
	if err != nil {
		return nil, err
	}

	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		now := timestamppb.New(time.Now())
		artifact.CreateTime = now
		artifact.UpdateTime = now

		// TODO(b/517479143): Consider moving ArtifactProtoToInsertMutation from spannerdata package into the artifact repository package.
		artifactMutation, err := spannerdata.ArtifactProtoToInsertMutation(artifact)
		if err != nil {
			logging.Error(ctx, "Failed to create mutation for artifact", "artifact", artifact, "error", err)
			return err
		}

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
