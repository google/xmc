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

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// DeleteArtifact deletes an artifact by its unique ID, together with its ArtifactCharts rows,
// which cascade through their foreign key as the interleaved child table does in Spanner. It
// returns a NotFound error when no artifact has the ID.
func (r *Repository) DeleteArtifact(ctx context.Context, aid int64) error {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	logging.Info(ctx, "Delete artifact in storage")

	// The unique ArtifactsByArtifactId index makes the ID sufficient to address the row, so one
	// statement replaces Spanner's read-then-delete transaction; its row count tells whether the
	// artifact existed.
	tag, err := r.db.Exec(ctx, deleteArtifactSQL.String(), aid)
	if err != nil {
		logging.Error(ctx, "Failed to delete artifact", "error", err)
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		err := pgdb.WrapStatusError(fmt.Sprintf("artifact %d not found", aid), grpcerr.ErrNotFound, nil)
		logging.Error(ctx, "Failed to delete artifact", "error", err)
		return err
	}

	logging.Info(ctx, "Deleted artifact from storage")

	return nil
}
