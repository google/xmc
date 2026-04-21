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
