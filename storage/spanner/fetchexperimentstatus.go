package spanner

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
	"xmanager_cloud/validator/validator"
	"task/go/status"
)

// FetchExperimentStatus retrieves the ExperimentStatus for a given XID.
func (s *Storage) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	return s.fetchExperimentStatusTransaction(ctx, xid, s.client.Single())
}

func (s *Storage) fetchExperimentStatusTransaction(ctx context.Context, xid int64, txn spannerTransaction) (*essmsgpb.ExperimentStatus, error) {
	logging.Info(ctx, "Fetch ExperimentStatus row")

	row, err := txn.ReadRow(ctx, experimentschema.ExperimentTableName, spanner.Key{reverseBits(xid)}, []string{"Status"})
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			return nil, fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Failed to read Experiment row", "error", err)
		return nil, fmt.Errorf("failed to read Experiment row: %w", err)
	}

	var status essmsgpb.ExperimentStatus
	if err := row.ColumnByName("Status", &status); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}

	logging.Info(ctx, "Fetched ExperimentStatus from storage", "status", &status)

	return &status, nil
}
