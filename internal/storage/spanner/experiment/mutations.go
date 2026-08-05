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

package experiment

import (
	"context"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/data/spannerdata"
)

// InsertExperiment buffers a Spanner insert mutation for the experiment within a transaction.
func (r *Repository) InsertExperiment(ctx context.Context, tx *spanner.ReadWriteTransaction, exp *essmsgpb.Experiment) error {
	mutation, err := spannerdata.ExperimentProtoToInsertMutation(exp)
	if err != nil {
		return err
	}
	return tx.BufferWrite([]*spanner.Mutation{mutation})
}

// GetExperiment retrieves a single Experiment from storage by its unique ID within a transaction.
func (r *Repository) GetExperiment(ctx context.Context, tx SpannerTransaction, xid int64) (*essmsgpb.Experiment, error) {
	return r.FetchExperimentTransaction(ctx, xid, tx)
}
