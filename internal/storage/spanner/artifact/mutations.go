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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/data/spannerdata"
)

// InsertArtifact buffers a Spanner insert mutation for the artifact within a transaction.
func (r *Repository) InsertArtifact(ctx context.Context, tx *spanner.ReadWriteTransaction, artifact *essmsgpb.Artifact) error {
	mutation, err := spannerdata.ArtifactProtoToInsertMutation(artifact)
	if err != nil {
		return err
	}
	return tx.BufferWrite([]*spanner.Mutation{mutation})
}
