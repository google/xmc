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

	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

// FetchArtifact retrieves a single Artifact from storage by its unique numerical ID, through the
// unique ArtifactsByArtifactId index. It returns a NotFound error when no artifact has the ID.
func (r *Repository) FetchArtifact(ctx context.Context, aid int64) (*pb.Artifact, error) {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	logging.Info(ctx, "Fetch artifact row")

	artifact, err := fetchArtifact(ctx, r.db, aid, false)
	if err != nil {
		logging.Error(ctx, "Failed to read artifact row", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Fetched artifact from storage", "artifact", artifact)

	return artifact, nil
}
