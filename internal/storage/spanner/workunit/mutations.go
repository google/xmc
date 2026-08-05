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

package workunit

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/data/spannerdata"
)

// InsertWorkUnit buffers a Spanner insert mutation for the work unit within a transaction.
func (r *Repository) InsertWorkUnit(ctx context.Context, tx *spanner.ReadWriteTransaction, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return fmt.Errorf("work unit must not be nil")
	}
	if tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	mutation, err := spannerdata.WorkUnitProtoToInsertMutation(wu)
	if err != nil {
		return err
	}
	return tx.BufferWrite([]*spanner.Mutation{mutation})
}

// FetchNextWorkUnitID retrieves the next available WorkUnit ID for a given XID within a transaction.
func (r *Repository) FetchNextWorkUnitID(ctx context.Context, tx *spanner.ReadWriteTransaction, xid int64) (int64, error) {
	return r.FetchNextWorkUnitIDTransaction(ctx, xid, tx)
}
