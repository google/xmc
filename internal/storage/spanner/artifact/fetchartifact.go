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
	"strings"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/artifactschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

// FetchArtifact retrieves a single Artifact from storage by its unique numerical ID.
func (r *Repository) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	return fetchArtifactInTx(ctx, aid, r.client.Single())
}

// FetchArtifact retrieves a single Artifact from storage by its unique numerical ID within the bound transaction.
func (r *TxRepository) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	return fetchArtifactInTx(ctx, aid, r.tx)
}

func fetchArtifactInTx(ctx context.Context, aid int64, txn spannerTransaction) (*essmsgpb.Artifact, error) {
	logging.Info(ctx, "Fetch artifact row")

	stmt := generateFetchArtifactStatement(aid)
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err == iterator.Done {
		return nil, fmt.Errorf("artifact %d not found: %w", aid, grpcerr.ErrNotFound)
	}
	if err != nil {
		logging.Error(ctx, "Failed to read artifact row", "error", err)
		return nil, fmt.Errorf("failed to read Artifact row: %w", err)
	}

	artifact, err := spannerdata.ArtifactRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Artifact row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched artifact from storage", "artifact", artifact)

	return artifact, nil
}

// For some reason, the emulator doesn't like it when we try to fetch artifacts by their primary key
// from a specific index using the Golang functions. So we need to write a DML for it instead, which
// seems to work fine. I have no idea why this would be the case, the emulator SHOULD support
// secondary indices, but this is a pretty simple fix and debugging the problem seems like a waste
// of time.
func generateFetchArtifactStatement(aid int64) spanner.Statement {
	return spanner.Statement{
		SQL: `SELECT ` + strings.Join(artifactschema.ArtifactColumns, ", ") + `
					FROM Artifacts@{FORCE_INDEX=` + artifactschema.ArtifactsByBitReversedArtifactID + `}
					WHERE BitReversedArtifactId = @id
					LIMIT 1`,
		Params: map[string]any{
			"id": spannerutils.ReverseBits(aid),
		},
	}
}
