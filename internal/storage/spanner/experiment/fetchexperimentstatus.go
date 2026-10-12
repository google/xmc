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
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/storage/spanner/schema/experimentschema"
	"github.com/google/xmc/internal/storage/spanner/utils"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FetchExperimentStatus retrieves the ExperimentStatus for a given XID.
func (r *Repository) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()
	expStatus, err := r.fetchExperimentStatusTransaction(ctx, xid, txn)
	if err != nil {
		return nil, err
	}
	if err := r.evaluateExperimentDrift(ctx, txn, xid, expStatus); err != nil {
		logging.Warn(ctx, "Failed to evaluate experiment drift", "xid", xid, "error", err)
	}
	return expStatus, nil
}

func (r *Repository) fetchExperimentStatusTransaction(ctx context.Context, xid int64, txn SpannerTransaction) (*essmsgpb.ExperimentStatus, error) {
	logging.Info(ctx, "Fetch ExperimentStatus row")

	row, err := txn.ReadRow(ctx, experimentschema.ExperimentTableName, spanner.Key{spannerutils.ReverseBits(xid)}, []string{"Status"})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("experiment %d not found: %w", xid, grpcerr.ErrNotFound)
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
