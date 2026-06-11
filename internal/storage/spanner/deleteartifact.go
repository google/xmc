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
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/schema/artifactschema"
	"xmanager_cloud/validator/validator"
)

// DeleteArtifact deletes an artifact by its unique ID.
func (s *Storage) DeleteArtifact(ctx context.Context, aid int64) error {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	logging.Info(ctx, "Delete artifact in storage")

	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		// 1. Read the existing artifact from storage so we can get the primary key.
		artifact, err := s.fetchArtifactTransaction(ctx, aid, tx)
		if err != nil {
			return err
		}

		// 2. Delete by mutation on the primary key. We can technically delete this by just the artifact
		// ID, but the emulator doesn't like that. So we include the XID and WID in the primary key.
		pk, err := generateArtifactPrimaryKey(artifact)
		if err != nil {
			return err
		}
		m := spanner.Delete(artifactschema.ArtifactTableName, pk)
		tx.BufferWrite([]*spanner.Mutation{m})
		return nil
	})
	if err != nil {
		logging.Error(ctx, "Failed to delete artifact", "error", err)
		return err
	}

	logging.Info(ctx, "Deleted artifact from storage")

	return nil
}

// generateArtifactPrimaryKey generates the primary key for an artifact from its proto.
func generateArtifactPrimaryKey(artifact *essmsgpb.Artifact) (spanner.Key, error) {
	aid, err := validator.ValidateArtifactName(artifact.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to parse artifact name %s: %w", artifact.Name, err)
	}

	xid, wid, err := validator.ValidateArtifactParent(artifact.Parent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse artifact parent %s: %w", artifact.Parent, err)
	}

	return spanner.Key{
		data.ReverseBits(xid),
		spanner.NullInt64{Int64: wid, Valid: wid != 0},
		aid,
	}, nil
}
