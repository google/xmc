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

	xpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/auth"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/validator"
)

// CheckUserAuthorization checks whether a user has access to an XID.
func (s *XIDServer) CheckUserAuthorization(ctx context.Context, in *xpb.CheckUserAuthorizationRequest) (*xpb.CheckUserAuthorizationResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Check user authorization", "name", in.Name)

	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	xid, err := validator.ValidateXIDName(in.GetName())
	if err != nil {
		logging.Warn(ctx, "Invalid XID name", "name", in.GetName(), "error", err)
		return nil, fmt.Errorf("invalid GetXid request %v: %w", err, grpcerr.ErrInvalidArgument)
	}
	ctx = logging.WithXIDName(ctx, in.GetName())

	// 3. Get the XidEntry from the storage layer.
	xidEntry, err := s.storage.FetchXidEntry(ctx, xid)
	if err != nil {
		if errors.Is(err, grpcerr.ErrNotFound) {
			logging.Warn(ctx, "XID not found", "error", err)
			return nil, fmt.Errorf("XID %d not found: %w", xid, grpcerr.ErrNotFound)
		}

		logging.Error(ctx, "Storage layer returned a non-gRPC error for FetchXidEntry", "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
	}

	// 4. Convert the role type to the internal representation.
	roleType := xidACLsToRoleType(in.GetRoleType())
	if roleType == auth.Unknown {
		logging.Warn(ctx, "Unknown role type", "role_type", in.GetRoleType())
		return nil, fmt.Errorf("unknown role type: %w", grpcerr.ErrInvalidArgument)
	}

	// 5. Check the user's authorization against the XidEntry ACLs.
	isAuthorized, err := s.checkUserAuthorization(ctx, email, xidEntry, roleType)
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Checked user authorization", "name", in.GetName(), "is_authorized", isAuthorized)
	return &xpb.CheckUserAuthorizationResponse{Authorized: &isAuthorized}, nil
}

func xidACLsToRoleType(roleType xpb.XidAcls_RoleType) auth.Role {
	switch roleType {
	case xpb.XidAcls_READER:
		return auth.Reader
	case xpb.XidAcls_WRITER:
		return auth.Writer
	case xpb.XidAcls_OWNER:
		return auth.Owner
	default:
		return auth.Unknown
	}
}

func (s *XIDServer) checkUserAuthorization(ctx context.Context, email string, xidEntry *xpb.XidEntry, roleType auth.Role) (bool, error) {
	// 1. Check the user's authorization against the XidEntry ACLs.
	isAuthorized, err := s.auth.IsUserAuthorized(ctx, email, xidEntry.Acls, roleType)
	if err != nil {
		logging.Warn(ctx, "Failed to check user authorization", "error", err)
		return false, fmt.Errorf("failed to check user authorization: %w", grpcerr.ErrPermissionDenied)
	}

	visited := make(map[string]bool)
	visited[xidEntry.GetName()] = true
	parentXIDName := xidEntry.GetParentXid()
	for !isAuthorized {
		// If the parent XID is empty, we're done.
		if parentXIDName == "" {
			break
		}

		if visited[parentXIDName] {
			logging.Warn(ctx, "Loop detected in XID parent chain", "xid_name", xidEntry.GetName())
			return false, fmt.Errorf("loop detected in XID parent chain: %w", grpcerr.ErrInternal)
		}
		visited[parentXIDName] = true

		// 2.1.  Check if the parent XID is valid.
		parentXID, err := validator.ValidateXIDName(parentXIDName)
		if err != nil {
			logging.Warn(ctx, "Invalid parent XID", "parent_xid_name", parentXIDName, "error", err)
			return false, fmt.Errorf("invalid parent XID %q: %w", parentXIDName, grpcerr.ErrInvalidArgument)
		}

		// 2.2. Get the parent XID from the storage layer.
		parentXIDEntry, err := s.storage.FetchXidEntry(ctx, parentXID)
		if err != nil {
			if errors.Is(err, grpcerr.ErrNotFound) {
				logging.Warn(ctx, "Parent XID not found", "parent_xid", parentXID, "error", err)
				return false, fmt.Errorf("parent XID %d not found: %w", parentXID, grpcerr.ErrNotFound)
			}

			logging.Error(ctx, "Storage layer returned a non-gRPC error for FetchXidEntry", "parent_xid", parentXID, "error", err)
			return false, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
		}

		// 2.3. Check the user's authorization against the parent XID ACLs.
		isAuthorized, err = s.auth.IsUserAuthorized(ctx, email, parentXIDEntry.Acls, roleType)
		if err != nil {
			logging.Warn(ctx, "Failed to check user authorization", "error", err)
			return false, fmt.Errorf("failed to check user authorization: %w", grpcerr.ErrPermissionDenied)
		}

		parentXIDName = parentXIDEntry.GetParentXid()
	}

	return isAuthorized, nil
}
