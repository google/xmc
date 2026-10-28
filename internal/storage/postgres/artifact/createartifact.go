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

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// CreateArtifact inserts a new artifact. The artifact ID comes from the artifact name, which the
// caller populates from the XID service, and the parent experiment or work unit from its parent.
// It returns the artifact proto with CreateTime and UpdateTime set to the insertion time. Inserting
// an ID that already exists returns an AlreadyExists error, and a parent experiment that does not
// exist a FailedPrecondition error.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *pb.Artifact) (*pb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil")
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)

	// 1. Validate the artifact name and parent and extract the primary key.
	if _, err := validator.ValidateArtifactName(artifact.GetName()); err != nil {
		return nil, err
	}
	key, err := keyFromProto(artifact)
	if err != nil {
		logging.Error(ctx, "Failed to derive artifact key", "artifact", artifact, "error", err)
		return nil, err
	}

	// 2. Set the timestamps to the current time.
	now := timestamppb.New(storageNow())
	artifact.CreateTime = now
	artifact.UpdateTime = now

	// 3. Insert the row. A single statement is atomic, so no explicit transaction is needed.
	columnArgs, err := mutableColumnArgs(artifact)
	if err != nil {
		logging.Error(ctx, "Failed to encode artifact columns", "artifact", artifact, "error", err)
		return nil, err
	}
	args := append([]any{key.xid, key.workUnitID(), key.aid}, columnArgs...)
	args = append(args, artifact.GetCreateTime().AsTime(), artifact.GetUpdateTime().AsTime())
	if _, err := r.db.Exec(ctx, insertArtifactSQL.String(), args...); err != nil {
		logging.Error(ctx, "Failed to create artifact", "error", err)
		return nil, pgdb.TranslateError(err, entityName)
	}

	logging.Info(ctx, "Written artifact to storage")

	return artifact, nil
}
