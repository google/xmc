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

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
)

// InsertWorkUnit buffers a mutation to insert a new WorkUnit in Spanner using the bound read-write transaction.
func (r *TxRepository) InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error {
	if wu == nil {
		return fmt.Errorf("work unit must not be nil")
	}
	if r.tx == nil {
		return fmt.Errorf("transaction must not be nil")
	}
	mutation, err := spannerdata.WorkUnitProtoToInsertMutation(wu)
	if err != nil {
		return err
	}
	if err := r.tx.BufferWrite([]*spanner.Mutation{mutation}); err != nil {
		logging.Error(ctx, "failed to buffer write mutation for create work unit", "error", err)
		return err
	}
	return nil
}
