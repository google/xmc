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

package artifactlifecycle

import (
	"context"
	"fmt"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// workUnitArtifactsPageSize is the ListArtifacts page size used when reading all
// artifacts of a work unit.
const workUnitArtifactsPageSize = 100

// ListWorkUnitArtifacts returns every artifact attached to the work unit, following
// ListArtifacts page tokens until the last page.
func ListWorkUnitArtifacts(ctx context.Context, store storage.ArtifactStore, xid, wid int64) ([]*essmsgpb.Artifact, error) {
	parent := validator.ConstructWorkUnitName(xid, wid)
	var all []*essmsgpb.Artifact
	pageToken := ""
	for {
		page, next, err := store.ListArtifacts(ctx, parent, workUnitArtifactsPageSize, pageToken, "", "", 0)
		if err != nil {
			return nil, fmt.Errorf("list artifacts for %s: %w", parent, err)
		}
		all = append(all, page...)
		if next == "" {
			return all, nil
		}
		pageToken = next
	}
}
