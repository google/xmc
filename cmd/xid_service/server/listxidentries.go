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

	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"

	"xmanager_cloud/internal/validator"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

const (
	// defaultXidPageSize is the default page size when page_size is omitted (0) in ListXidEntriesRequest.
	defaultXidPageSize = 10
	// maxPageSize is the maximum page size that can be specified in a ListXidEntriesRequest.
	// We guarantee that this is at least as large as the largest page size used by any client.
	maxPageSize = 100000
)

// ListXidEntries handles the RPC for listing and filtering XidEntries.
func (s *XIDServer) ListXidEntries(ctx context.Context, in *apipb.ListXidEntriesRequest) (*apipb.ListXidEntriesResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	return s.listXidEntriesWithStaleness(ctx, in, 0*time.Second)
}

func (s *XIDServer) SearchXidEntries(ctx context.Context, in *apipb.ListXidEntriesRequest) (*apipb.ListXidEntriesResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	return s.listXidEntriesWithStaleness(ctx, in, 15*time.Second)
}

func (s *XIDServer) listXidEntriesWithStaleness(ctx context.Context, in *apipb.ListXidEntriesRequest, staleness time.Duration) (*apipb.ListXidEntriesResponse, error) {
	// 1. Authenticate the user.
	logging.Info(ctx, "Fetch XID entries", "request", in, "staleness", staleness)
	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if err := validator.ValidateAIPCompliantListWithMaxPageSize(in.GetPageSize(), in.GetFilter(), in.GetOrderBy(), in.GetPageToken(), maxPageSize); err != nil {
		logging.Warn(ctx, "Invalid ListXidEntriesRequest", "error", err)
		return nil, fmt.Errorf("invalid ListXidEntriesRequest %v: %w", err, grpcerr.ErrInvalidArgument)
	}

	pageSize := in.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultXidPageSize
	}

	// 3. Get the list of XidEntries from the storage layer.
	xids, nextPageToken, err := s.storage.ListXidEntries(ctx, pageSize, in.GetPageToken(), in.GetFilter(), in.GetOrderBy(), staleness)
	if err != nil {
		statusErr, ok := status.FromError(err)
		if ok {
			logging.Error(ctx, "Storage layer returned a gRPC error for listing XIDs", "error", err)
			return nil, status.Errorf(statusErr.Code(), "an error occurred while retrieving the XID %v", err)
		}

		logging.Error(ctx, "Storage layer returned a non-gRPC error for listing XIDs", "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the XID: %w", grpcerr.ErrInternal)
	}

	logging.Info(ctx, "Fetched XID entries", "count", len(xids), "nextPageToken", nextPageToken, "staleness", staleness)
	if len(xids) == 0 {
		logging.Warn(ctx, "No XID entries found for request", "request", in)
	}
	return &apipb.ListXidEntriesResponse{
		XidEntries:    xids,
		NextPageToken: &nextPageToken,
	}, nil
}
