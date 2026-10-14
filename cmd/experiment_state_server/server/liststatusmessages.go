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
	"fmt"
	"time"

	apipb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
)

const (
	defaultStatusMessagePageSize = 10
	maxStatusMessagePageSize     = 1000
	statusMessageSearchStaleness = 15 * time.Second
)

// ListStatusMessages returns a list of historical status messages for an experiment or work unit.
func (h *StatusMessageHandler) ListStatusMessages(ctx context.Context, req *apipb.ListStatusMessagesRequest) (*apipb.ListStatusMessagesResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch status messages", "request", req)
	resp, err := h.listStatusMessagesWithStaleness(ctx, req, 0*time.Second)
	logging.Info(ctx, "Fetched status messages", "response", resp)
	return resp, err
}

// SearchStatusMessages returns a list of historical status messages for an experiment or work unit
// with a specified staleness for potentially faster reads.
func (h *StatusMessageHandler) SearchStatusMessages(ctx context.Context, req *apipb.ListStatusMessagesRequest) (*apipb.ListStatusMessagesResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Search status messages", "request", req)
	resp, err := h.listStatusMessagesWithStaleness(ctx, req, statusMessageSearchStaleness)
	logging.Info(ctx, "Searched status messages", "response", resp)
	return resp, err
}

func (h *StatusMessageHandler) listStatusMessagesWithStaleness(ctx context.Context, req *apipb.ListStatusMessagesRequest, staleness time.Duration) (*apipb.ListStatusMessagesResponse, error) {
	// 1. Authenticate the user.
	logging.Debug(ctx, "Extracting user identity from context")
	email, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if req.GetPageSize() == 0 {
		logging.Warn(ctx, "Page size not specified, using default", "page_size", defaultStatusMessagePageSize)
		req.PageSize = defaultStatusMessagePageSize
	}
	if err := validator.ValidateAIPCompliantListWithMaxPageSize(req.GetPageSize(), req.GetFilter(), req.GetOrderBy(), req.GetPageToken(), maxStatusMessagePageSize); err != nil {
		logging.Warn(ctx, "Invalid ListStatusMessagesRequest", "error", err)
		return nil, fmt.Errorf("invalid ListStatusMessagesRequest: %w (%v)", grpcerr.ErrInvalidArgument, err)
	}

	// 3. Validate the parent.
	parent := req.GetParent()
	parentXID, _, err := validator.ValidateStatusMessageParent(parent)
	if err != nil {
		logging.Warn(ctx, "Failed to validate parent", "parent", parent, "error", err)
		return nil, fmt.Errorf("failed to validate parent %s: %w (%v)", parent, grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithStatusMessageParent(ctx, parent)

	// 4. Authorize the caller to get the status messages for this experiment or work unit.
	authorized, err := h.checkUserAuthorization(ctx, validator.ConstructXIDName(parentXID), email, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to list status messages", "user", email, "xid", parentXID)
		return nil, fmt.Errorf("user %s is not authorized to list status messages for XID %d: %w", email, parentXID, grpcerr.ErrPermissionDenied)
	}

	// 5. Get the list of StatusMessages from the storage layer.
	messages, nextPageToken, err := h.storage.ListStatusMessages(ctx, req.GetParent(), req.GetPageSize(), req.GetPageToken(), req.GetFilter(), req.GetOrderBy(), staleness)
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		logging.Warn(ctx, "No status messages found for request")
	}

	return &apipb.ListStatusMessagesResponse{
		StatusMessages: messages,
		NextPageToken:  nextPageToken,
	}, nil
}
