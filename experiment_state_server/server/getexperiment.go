package server

import (
	"context"
	"fmt"

	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	"task/go/status"

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// GetExperiment orchestrates the retrieval of a single experiment by its ID.
func (s *Server) GetExperiment(ctx context.Context, req *apipb.GetExperimentRequest) (*msgpb.Experiment, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch experiment", "request", req)

	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	name := req.GetName()
	xid, err := validator.ValidateExperimentName(name)
	if err != nil {
		logging.Warn(ctx, "GetExperiment validation failed", "name", name, "error", err)
		return nil, fmt.Errorf("GetExperiment validation failed: %w (%v)", status.ErrInvalidArgument, err)
	}
	ctx = logging.WithExperimentName(ctx, name)

	// 3. Retrieve the XID entry from the XID service. This is for authorization purposes and to get
	// the ACLs.
	xidEntry, err := s.getXid(ctx, validator.ConstructXIDName(xid))
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.PermissionDenied {
			logging.Warn(ctx, "User not authorized to get experiment", "error", err)
			return nil, fmt.Errorf("user not authorized to get experiment %s: %w (%v)", name, status.ErrPermissionDenied, err)
		}
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Experiment not found in XID service")
			return nil, fmt.Errorf("experiment with name %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		logging.Error(ctx, "XID lookup failed", "error", err)
		return nil, fmt.Errorf("XID lookup failed for XID %d: %w (%v)", xid, status.ErrInternal, err)
	}

	// 4. Retrieve the experiment from the storage layer.
	logging.Debug(ctx, "Fetching experiment from storage")
	exp, err := s.storage.FetchExperiment(ctx, xid)
	if err != nil {
		if status.ExtractCanonicalCode(err) == status.NotFound {
			logging.Warn(ctx, "Experiment not found in storage")
			return nil, fmt.Errorf("experiment with name %s not found: %w (%v)", name, status.ErrNotFound, err)
		}

		logging.Error(ctx, "Failed to retrieve experiment from storage", "error", err)
		return nil, fmt.Errorf("failed to retrieve experiment %s from storage: %w (%v)", name, status.ErrInternal, err)
	}

	// 4.1. Attach the ACLs from the XID entry to the experiment.
	exp.Acls = xidEntry.GetAcls()

	// 5. Return the successful response.
	logging.Info(ctx, "Fetched experiment", "experiment", exp)
	return exp, nil
}
