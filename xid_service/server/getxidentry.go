package server

import (
	"context"
	"errors"
	"fmt"

	"xmanager_cloud/auth/auth"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

// GetXidEntry handles the RPC for retrieving a single XidEntry.
func (s *XIDServer) GetXidEntry(ctx context.Context, in *apipb.GetXidEntryRequest) (*msgpb.XidEntry, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch XID entry", "name", in.GetName())

	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, err := validator.ValidateXIDName(in.GetName())
	if err != nil {
		logging.Warn(ctx, "Invalid GetXidEntry request", "error", err)
		return nil, fmt.Errorf("invalid GetXid request %v: %w", err, status.ErrInvalidArgument)
	}
	ctx = logging.WithXIDName(ctx, in.GetName())

	// 3. Get the XidEntry from the storage layer.
	xidEntry, err := s.storage.FetchXidEntry(ctx, xid)
	if err != nil {
		if errors.Is(err, status.ErrNotFound) {
			logging.Warn(ctx, "XID not found", "xid", xid, "error", err)
			return nil, fmt.Errorf("XID %d not found: %w", xid, status.ErrNotFound)
		}

		logging.Error(ctx, "Storage layer returned a non-gRPC error for FetchXidEntry", "xid", xid, "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the XID: %w", status.ErrInternal)
	}

	// 4. Authorize the user.
	authorized, err := s.checkUserAuthorization(ctx, email, xidEntry, auth.Reader)
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User is not authorized to read XID", "xid", xid, "email", email)
		return nil, fmt.Errorf("user is not authorized to read XID %d: %w", xid, status.ErrPermissionDenied)
	}
	logging.Info(ctx, "Fetched XID entry", "name", in.GetName())

	return xidEntry, nil
}
