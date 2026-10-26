// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/jackc/pgx/v5"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// UpdateArtifact updates an artifact with the given update mask. We return both the original and
// updated artifacts, so that the caller can choose whether to perform additional actions based on
// the original artifact state.
//
// The read-modify-write runs in a transaction of its own: the existing row is read with
// FOR NO KEY UPDATE, so that concurrent updates of the same artifact are serialized instead of
// overwriting each other, merged with the update through data.MergeArtifacts and written back only
// when the merge changed it, in which case UpdateTime is refreshed. The transaction is retried on
// serialization failures and deadlocks.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	if update == nil {
		return storage.UpdatedArtifacts{}, fmt.Errorf("update must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(update.AID))
	logging.Info(ctx, "Update artifact in storage", "update", update.Artifact)

	var originalArtifact, updatedArtifact *pb.Artifact
	var mutated bool
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		// The callback may run more than once, so reset everything it captures.
		originalArtifact, updatedArtifact, mutated = nil, nil, false

		// 1. Read and lock the existing artifact.
		var err error
		originalArtifact, err = fetchArtifact(ctx, tx, update.AID, true)
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
		updatedArtifact.UpdateTime = timestamppb.New(storageNow())

		// 3. Write the merged artifact back to its row.
		key, err := keyFromProto(updatedArtifact)
		if err != nil {
			return err
		}
		columnArgs, err := mutableColumnArgs(updatedArtifact)
		if err != nil {
			return err
		}
		args := append([]any{key.xid, key.aid}, columnArgs...)
		args = append(args, updatedArtifact.GetUpdateTime().AsTime())
		tag, err := tx.Exec(ctx, updateArtifactSQL.String(), args...)
		if err != nil {
			return pgdb.TranslateError(err, entityName)
		}
		if tag.RowsAffected() == 0 {
			// The locked read guarantees the row exists for the rest of the transaction.
			return pgdb.WrapStatusError(fmt.Sprintf("artifact %d not found", update.AID), grpcerr.ErrNotFound, nil)
		}
		mutated = true
		return nil
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
