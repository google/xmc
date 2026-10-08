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
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

// UpdateArtifact updates an artifact with the given update mask. We return both the original and updated artifacts,
// so that the caller can choose whether to perform additional actions based on the original artifact state.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	if update == nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(update.AID))
	logging.Info(ctx, "Update artifact in storage", "update", update.Artifact)

	var originalArtifact, updatedArtifact *essmsgpb.Artifact
	var mutated bool
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		mutated = false
		// 1. Read the existing artifact from storage.
		var err error
		originalArtifact, err = r.FetchArtifactTransaction(ctx, update.AID, txn)
		if err != nil {
			return err
		}

		// 2. Merge the current and new artifacts based on the update mask.
		updatedArtifact, err = data.MergeArtifacts(originalArtifact, update.Artifact, update.UpdateMask)
		if err != nil {
			return fmt.Errorf("failed to merge Artifact: %w", err)
		}

		// 2.1. Check if the artifact has changed.
		if proto.Equal(originalArtifact, updatedArtifact) {
			return nil
		}

		// 2.2. Update the UpdateTime column to the current time.
		now := time.Now()
		updatedArtifact.UpdateTime = timestamppb.New(now)

		// 3. Construct the Spanner mutation for the update.
		mutation, err := spannerdata.ArtifactProtoToUpdateMutation(updatedArtifact)
		if err != nil {
			return fmt.Errorf("failed to create Spanner mutation for Artifact: %w", err)
		}

		// 4. Apply the mutation to the transaction.
		mutated = true
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	if err != nil {
		return storage.UpdatedArtifacts{}, err
	}

	if mutated {
		protoDiff := diff.Diff(prototext.Format(originalArtifact), prototext.Format(updatedArtifact))
		logging.Info(ctx, fmt.Sprintf("Updated artifact in storage: %s", protoDiff), "updatedArtifact", updatedArtifact)
	} else {
		logging.Debug(ctx, "Artifact not updated; no changes detected", "updatedArtifact", updatedArtifact)
	}

	return storage.UpdatedArtifacts{
		Original: originalArtifact,
		Updated:  updatedArtifact,
	}, nil
}
