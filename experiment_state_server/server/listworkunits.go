// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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

	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
	"task/go/status"
)

const (
	// defaultWorkUnitPageSize is the number of results returned if the client
	// does not specify a page_size.
	defaultWorkUnitPageSize = 10
	workUnitSearchStaleness = 15 * time.Second
)

func (s *Server) ListWorkUnits(ctx context.Context, req *apipb.ListWorkUnitsRequest) (*apipb.ListWorkUnitsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch work units", "request", req)
	resp, err := s.listWorkUnitsWithStaleness(ctx, req, 0*time.Second)
	logging.Info(ctx, "Fetched work units", "response", resp)
	return resp, err
}

func (s *Server) SearchWorkUnits(ctx context.Context, req *apipb.ListWorkUnitsRequest) (*apipb.ListWorkUnitsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Search work units", "request", req)
	resp, err := s.listWorkUnitsWithStaleness(ctx, req, workUnitSearchStaleness)
	logging.Info(ctx, "Searched work units", "response", resp)
	return resp, err
}

func (s *Server) listWorkUnitsWithStaleness(ctx context.Context, req *apipb.ListWorkUnitsRequest, staleness time.Duration) (*apipb.ListWorkUnitsResponse, error) {
	// 1. Authenticate the user.
	email, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if req.GetPageSize() == 0 {
		logging.Warn(ctx, "Page size not specified, using default", "page_size", defaultWorkUnitPageSize)
		req.PageSize = defaultWorkUnitPageSize
	}
	if err := validator.ValidateAIPCompliantList(req.GetPageSize(), req.GetFilter(), req.GetOrderBy(), req.GetPageToken()); err != nil {
		logging.Warn(ctx, "Invalid ListWorkUnitsRequest", "error", err)
		return nil, fmt.Errorf("invalid ListWorkUnitsRequest: %w", status.ErrInvalidArgument)
	}

	// 2.1. Get the XID from the request.
	xid, err := validator.ValidateExperimentName(req.GetParent())
	if err != nil {
		logging.Warn(ctx, "Invalid parent name", "error", err)
		return nil, fmt.Errorf("invalid parent name: %w", status.ErrInvalidArgument)
	}
	ctx = logging.WithExperimentName(ctx, req.GetParent())

	// 3. Authorize the caller to get the work units for this experiment.
	authorized, err := s.checkUserAuthorization(ctx, validator.ConstructXIDName(xid), email, xidmsgpb.XidAcls_READER.Enum())
	if err != nil {
		return nil, err
	}
	if !authorized {
		logging.Warn(ctx, "User not authorized to list work units", "user", email, "xid", xid)
		return nil, fmt.Errorf("user %s is not authorized to list work units for XID %d: %w", email, xid, status.ErrPermissionDenied)
	}

	// 3. Get the list of WorkUnits from the storage layer.
	workUnits, nextPageToken, err := s.storage.ListWorkUnits(ctx, req.GetParent(), req.GetPageSize(), req.GetPageToken(), req.GetFilter(), req.GetOrderBy(), staleness, s.cfg.DefaultToCursorPagination)
	if err != nil {
		statusErr, ok := status.FromError(err)
		if !ok {
			logging.Error(ctx, "Storage layer returned a non-gRPC error for listing WorkUnits", "error", err)
			return nil, fmt.Errorf("an internal error occurred while retrieving the WorkUnits: %w", status.ErrInternal)
		}

		if statusErr.Code == status.InvalidArgument {
			logging.Error(ctx, "Storage layer returned an invalid argument error for listing WorkUnits", "error", err)
			return nil, fmt.Errorf("invalid ListWorkUnitsRequest %v: %w", err, status.ErrInvalidArgument)
		}

		logging.Error(ctx, "Storage layer returned a gRPC error for listing WorkUnits", "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the WorkUnits: %w", status.ErrInternal)
	}

	// 4. Return the successful response.
	return &apipb.ListWorkUnitsResponse{
		WorkUnits:     workUnits,
		NextPageToken: nextPageToken,
	}, nil
}
