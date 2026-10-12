// Copyright 2025 Google LLC
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
package server

import (
	"context"
	"errors"
	"fmt"

	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func (s *XIDServer) UpdateXidEntry(ctx context.Context, in *apipb.UpdateXidEntryRequest) (*msgpb.XidEntry, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Update XID entry", "request", in)

	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, err := validator.ValidateXIDName(in.XidEntry.GetName())
	if err != nil {
		logging.Warn(ctx, "Invalid UpdateXidEntry request", "error", err)
		return nil, fmt.Errorf("invalid UpdateXidEntry request %v: %w", err, grpcerr.ErrInvalidArgument)
	}
	ctx = logging.WithXIDName(ctx, in.XidEntry.GetName())

	// 3. Get the XidEntry from the storage layer.
	xidEntry, err := s.storage.FetchXidEntry(ctx, xid)
	if err != nil {
		if errors.Is(err, grpcerr.ErrNotFound) {
			logging.Warn(ctx, "XID not found", "xid", xid, "error", err)
			return nil, fmt.Errorf("XID %d not found: %w", xid, grpcerr.ErrNotFound)
		}

		logging.Error(ctx, "Storage layer error for FetchXidEntry", "xid", xid, "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
	}

	// 3. Authorize the user.
	isAuthorized, err := s.checkUserAuthorization(ctx, email, xidEntry, auth.Owner)
	if err != nil {
		return nil, err
	}
	if !isAuthorized {
		return nil, fmt.Errorf("user is not authorized to update XID %d: %w", xid, grpcerr.ErrPermissionDenied)
	}

	// 4. Update the XidEntry in the storage layer.
	updatedEntry, err := s.storage.UpdateXidEntry(ctx, &storage.XidEntryUpdate{
		XID:        xid,
		Creator:    email,
		XidEntry:   in.XidEntry,
		UpdateMask: in.UpdateMask,
	})
	if err != nil {
		if errors.Is(err, grpcerr.ErrNotFound) {
			logging.Warn(ctx, "XID not found", "xid", xid, "error", err)
			return nil, fmt.Errorf("XID %d not found: %w", xid, grpcerr.ErrNotFound)
		}
		if errors.Is(err, grpcerr.ErrInvalidArgument) {
			logging.Warn(ctx, "Invalid UpdateXidEntry request", "error", err)
			return nil, fmt.Errorf("invalid UpdateXidEntry request %v: %w", err, grpcerr.ErrInvalidArgument)
		}
		logging.Error(ctx, "Storage layer error for UpdateXidEntry", "xid", xid, "error", err)
		return nil, fmt.Errorf("an internal error occurred while updating the XID: %w", grpcerr.ErrInternal)
	}

	// 5. Return the updated XidEntry.
	logging.Info(ctx, "Updated XID entry", "xid", xid, "xid_entry", updatedEntry)
	return updatedEntry, nil
}
