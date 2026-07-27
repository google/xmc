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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
	"xmanager_cloud/validator/validator"
)

// FetchExperimentStatus retrieves the ExperimentStatus for a given XID.
func (r *Repository) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	return r.fetchExperimentStatusTransaction(ctx, xid, r.client.Single())
}

func (r *Repository) fetchExperimentStatusTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*essmsgpb.ExperimentStatus, error) {
	logging.Info(ctx, "Fetch ExperimentStatus row")

	row, err := txn.ReadRow(ctx, experimentschema.ExperimentTableName, spanner.Key{reverseBits(xid)}, []string{"Status"})
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Experiment row", "error", err)
		return nil, fmt.Errorf("failed to read Experiment row: %w", err)
	}

	var expStatus essmsgpb.ExperimentStatus
	if err := row.ColumnByName("Status", &expStatus); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}

	logging.Info(ctx, "Fetched ExperimentStatus from storage", "status", &expStatus)

	return &expStatus, nil
}
