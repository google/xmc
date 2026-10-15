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
	"fmt"

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
)

var _ storage.ExperimentTxRepository = (*TxRepository)(nil)

// InsertExperiment buffers a mutation to insert an Experiment in Spanner using the bound read-write transaction.
func (r *TxRepository) InsertExperiment(ctx context.Context, exp *essmsgpb.Experiment) error {
	if exp == nil {
		return fmt.Errorf("experiment must not be nil")
	}
	expMutation, err := spannerdata.ExperimentProtoToInsertMutation(exp)
	if err != nil {
		logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{expMutation})
}
