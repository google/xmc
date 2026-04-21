package spanner

import (
	"context"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/validator/validator"
)

// CreateExperiment creates a new experiment in Spanner. It returns the experiment proto with
// the timestamps set to the initial commit timestamps.
func (s *Storage) CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, exp.GetName())
	logging.Info(ctx, "Write experiment to storage", "experiment", exp)

	// 1. Validate the experiment name and extract the XID.
	xid, err := validator.ValidateExperimentName(exp.Name)
	if err != nil {
		return nil, err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		// 2. Set the timestamps to the current time.
		now := timestamppb.New(time.Now())
		exp.CreateTime = now
		exp.UpdateTime = now
		exp.Status.LastChildStatusUpdateTime = now
		exp.Status.UpdateTime = now

		// 3. Generate the insert mutation for the experiment.
		expMutation, err := spannerdata.ExperimentProtoToInsertMutation(exp)
		if err != nil {
			logging.Error(ctx, "failed to create mutation for experiment", "experiment", exp, "error", err)
			return err
		}

		// 4. Buffer the write mutation.
		err = tx.BufferWrite([]*spanner.Mutation{expMutation})
		if err != nil {
			logging.Error(ctx, "failed to buffer write mutation for create experiment", "error", err)
			return err
		}

		s.saveStatusMessageHistoryIfChanged(ctx, xid, nil, exp.GetStatus().GetStructuredStatusMessage(), nil, tx)

		return nil
	})

	if err != nil {
		logging.Error(ctx, "failed to create experiment", "error", err)
		return nil, err
	}

	logging.Info(ctx, "Written experiment to storage")

	return exp, nil
}
