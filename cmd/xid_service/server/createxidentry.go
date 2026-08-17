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

	"task/go/status"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func (s *XIDServer) CreateXidEntry(ctx context.Context, in *apipb.CreateXidEntryRequest) (*msgpb.XidEntry, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Create XID entry", "request", in)

	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if err := validator.ValidateCreateXidRequest(in); err != nil {
		logging.Warn(ctx, "Invalid CreateXidEntry request", "error", err)
		return nil, fmt.Errorf("invalid CreateXid request: %w", err)
	}

	// 2.1. Set the creator field in the request payload to the authenticated user.
	in.XidEntry.Creator = &email

	// 2.2. Fill in any null values in the request payload.
	in.XidEntry = validator.FillXidEntryNullValues(in.GetXidEntry())

	// 3. Create the XID in the storage layer.
	createdEntry, err := s.storage.CreateXidEntry(ctx, in.GetXidEntry())
	if err != nil {
		logging.Error(ctx, "Storage layer returned a non-gRPC error for CreateXidEntry", "error", err)
		return nil, fmt.Errorf("an internal error occurred while creating the XID: %v: %w", err, status.ErrInternal)
	}

	ctx = logging.WithXIDName(ctx, createdEntry.GetName())
	logging.Info(ctx, "Created XID entry", "xid_name", createdEntry.GetName(), "creator", email)
	return createdEntry, nil
}
