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
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/validator"
)

// UpdateArtifact updates an artifact with the given update mask within the bound read-write transaction.
func (r *TxRepository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	return updateArtifactInTx(ctx, r.tx, update)
}

// UpdateArtifact updates an artifact with the given update mask. We return both the original and updated artifacts,
// so that the caller can choose whether to perform additional actions based on the original artifact state.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	if update == nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(update.AID))
	logging.Info(ctx, "Update artifact in storage", "update", update.Artifact)

	var res storage.UpdatedArtifacts
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		var err error
		res, err = updateArtifactInTx(ctx, txn, update)
		return err
	})
	if err != nil {
		return storage.UpdatedArtifacts{}, err
	}

	return res, nil
}

func updateArtifactInTx(ctx context.Context, tx *spanner.ReadWriteTransaction, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	if update == nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("update must not be nil")
	}

	// 1. Read the existing artifact from storage.
	originalArtifact, err := fetchArtifactInTx(ctx, update.AID, tx)
	if err != nil {
		return storage.UpdatedArtifacts{}, err
	}

	// 2. Merge the current and new artifacts based on the update mask.
	updatedArtifact, err := data.MergeArtifacts(originalArtifact, update.Artifact, update.UpdateMask)
	if err != nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("failed to merge Artifact: %w", err)
	}

	// 2.1. Check if the artifact has changed.
	if proto.Equal(originalArtifact, updatedArtifact) {
		logging.Debug(ctx, "Artifact not updated; no changes detected", "updatedArtifact", updatedArtifact)
		return storage.UpdatedArtifacts{
			Original: originalArtifact,
			Updated:  updatedArtifact,
		}, nil
	}

	// 2.2. Update the UpdateTime column to the current time.
	now := time.Now()
	updatedArtifact.UpdateTime = timestamppb.New(now)

	// 3. Construct the Spanner mutation for the update.
	mutation, err := spannerdata.ArtifactProtoToUpdateMutation(updatedArtifact)
	if err != nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("failed to create Spanner mutation for Artifact: %w", err)
	}

	// 4. Apply the mutation to the transaction.
	if err := tx.BufferWrite([]*spanner.Mutation{mutation}); err != nil {
		return storage.UpdatedArtifacts{}, err
	}

	protoDiff := diff.Diff(prototext.Format(originalArtifact), prototext.Format(updatedArtifact))
	logging.Info(ctx, fmt.Sprintf("Updated artifact in storage: %s", protoDiff), "updatedArtifact", updatedArtifact)

	return storage.UpdatedArtifacts{
		Original: originalArtifact,
		Updated:  updatedArtifact,
	}, nil
}
