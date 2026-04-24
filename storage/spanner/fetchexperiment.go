// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package spanner

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// FetchExperiment retrieves a single Experiment from storage by its unique numerical ID.
func (s *Storage) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	return s.fetchExperimentTransaction(ctx, xid, s.client.Single())
}

// fetchExperimentTransaction retrieves a single Experiment from storage by its unique
// numerical ID in a transaction.
func (s *Storage) fetchExperimentTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*essmsgpb.Experiment, error) {
	logging.Info(ctx, "Fetch experiment row")

	row, err := txn.ReadRow(ctx, experimentschema.ExperimentTableName, spanner.Key{reverseBits(xid)}, experimentschema.ExperimentColumns)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Experiment row", "error", err)
		return nil, fmt.Errorf("failed to read Experiment row: %w", err)
	}

	exp, err := spannerdata.ExperimentRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Experiment row to proto: %w", err)
	}

	logging.Info(ctx, "Fetched experiment from storage", "experiment", exp)

	return exp, nil
}
