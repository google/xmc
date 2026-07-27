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
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

// UpdateArtifact updates an artifact with the given update mask.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (originalArtifact *essmsgpb.Artifact, updatedArtifact *essmsgpb.Artifact, err error) {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(update.AID))
	logging.Info(ctx, "Update artifact in storage", "update", update.Artifact)

	var mutated bool
	_, err = r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		originalArtifact, err = r.FetchArtifactTransaction(ctx, update.AID, txn)
		if err != nil {
			return err
		}

		updatedArtifact, err = data.MergeArtifacts(originalArtifact, update.Artifact, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge Artifact: %w", err)
		}

		if proto.Equal(originalArtifact, updatedArtifact) {
			return nil
		}

		now := time.Now()
		updatedArtifact.UpdateTime = timestamppb.New(now)

		mutation, err := spannerdata.ArtifactProtoToUpdateMutation(updatedArtifact)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for Artifact: %w", err)
		}

		mutated = true
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		return nil, nil, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalArtifact), prototext.Format(updatedArtifact))
		logging.Info(ctx, fmt.Sprintf("Updated artifact in storage: %s", protoDiff), "updatedArtifact", updatedArtifact)
	} else {
		logging.Debug(ctx, "Artifact not updated; no changes detected", "updatedArtifact", updatedArtifact)
	}

	return originalArtifact, updatedArtifact, nil
}
