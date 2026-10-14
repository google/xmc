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
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	apipb "xmanager_cloud/api/protos/experiment"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/validator"
)

const (
	// defaultExperimentPageSize is the number of results returned if the client
	// does not specify a page_size.
	defaultExperimentPageSize = 10
	experimentSearchStaleness = 15 * time.Second
)

func (h *ExperimentHandler) ListExperiments(ctx context.Context, req *apipb.ListExperimentsRequest) (*apipb.ListExperimentsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch experiments", "request", req)
	resp, err := h.listExperimentsWithStaleness(ctx, req, 0*time.Second)
	logging.Info(ctx, "Fetched experiments", "response", resp)
	return resp, err
}

func (h *ExperimentHandler) SearchExperiments(ctx context.Context, req *apipb.ListExperimentsRequest) (*apipb.ListExperimentsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Search experiments", "request", req)
	resp, err := h.listExperimentsWithStaleness(ctx, req, experimentSearchStaleness)
	logging.Info(ctx, "Searched experiments", "response", resp)
	return resp, err
}

func (h *ExperimentHandler) listExperimentsWithStaleness(ctx context.Context, req *apipb.ListExperimentsRequest, staleness time.Duration) (*apipb.ListExperimentsResponse, error) {
	// 1. Authenticate the user.
	_, _, err := h.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if req.GetPageSize() == 0 {
		logging.Warn(ctx, "Page size not specified, using default", "page_size", defaultExperimentPageSize)
		req.PageSize = defaultExperimentPageSize
	}
	if err := validator.ValidateAIPCompliantList(req.GetPageSize(), req.GetFilter(), req.GetOrderBy(), req.GetPageToken()); err != nil {
		logging.Warn(ctx, "Invalid ListExperimentsRequest", "error", err)
		return nil, fmt.Errorf("invalid ListExperimentsRequest: %w", grpcerr.ErrInvalidArgument)
	}

	// 3. Get the list of Experiments from the storage layer.
	experiments, nextPageToken, err := h.storage.ListExperiments(ctx, req.GetPageSize(), req.GetPageToken(), req.GetFilter(), req.GetOrderBy(), staleness)
	if err != nil {
		statusErr, ok := status.FromError(err)
		if !ok {
			logging.Error(ctx, "Storage layer returned a non-gRPC error for listing Experiments", "error", err)
			return nil, fmt.Errorf("an internal error occurred while retrieving the Experiments: %w", grpcerr.ErrInternal)
		}

		if statusErr.Code() == codes.InvalidArgument {
			logging.Error(ctx, "Storage layer returned an invalid argument error for listing Experiments", "error", err)
			return nil, fmt.Errorf("invalid ListExperimentsRequest %v: %w", err, grpcerr.ErrInvalidArgument)
		}

		logging.Error(ctx, "Storage layer returned a gRPC error for listing Experiments", "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the Experiments: %w", grpcerr.ErrInternal)
	}

	// 4. Populate ACLs from XID service.
	if err := h.fetchACLsForExperiments(ctx, experiments, req.GetPageSize()); err != nil {
		return nil, err
	}

	// 5. Return the successful response.
	return &apipb.ListExperimentsResponse{
		Experiments:   experiments,
		NextPageToken: nextPageToken,
	}, nil
}

func (h *ExperimentHandler) fetchACLsForExperiments(ctx context.Context, experiments []*apipb.Experiment, pageSize int32) error {
	if len(experiments) == 0 {
		logging.Warn(ctx, "No experiments fetched, skipping ACL population")
		return nil
	}
	// Generate a filter that will return the same experiments, but in the form of XIDs.
	var xidFilter []string
	for _, exp := range experiments {
		xid, err := validator.ValidateExperimentName(exp.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate experiment name", "error", err)
			return fmt.Errorf("failed to validate experiment name: %w", grpcerr.ErrInternal)
		}
		xidFilter = append(xidFilter, fmt.Sprintf("bit_reversed_xid=%d", data.ReverseBits(xid)))
	}

	// Call the XID service to get the XID entries.
	xidListResponse, err := h.xidClient.ListXidEntries(propagateIAPJWT(ctx), &xidpb.ListXidEntriesRequest{
		PageSize: proto.Int32(pageSize),
		Filter:   proto.String(strings.Join(xidFilter, " OR ")),
	})
	if err != nil {
		logging.Error(ctx, "Failed to list XIDs", "error", err)
		return fmt.Errorf("failed to list XIDs: %w", grpcerr.ErrInternal)
	}
	xidEntries := xidListResponse.GetXidEntries()

	// Build a map of XID to XID entry for quick lookup.
	xidMap := make(map[int64]*xidpb.XidEntry)
	for _, xidEntry := range xidEntries {
		xid, err := validator.ValidateXIDName(xidEntry.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate XID name", "name", xidEntry.GetName(), "error", err)
			return fmt.Errorf("failed to validate XID name: %w", grpcerr.ErrInternal)
		}
		xidMap[xid] = xidEntry
	}

	// Update the experiments with the XID entries.
	for _, exp := range experiments {
		xid, err := validator.ValidateExperimentName(exp.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate experiment name", "error", err)
			return fmt.Errorf("failed to validate experiment name: %w", grpcerr.ErrInternal)
		}
		xidEntry, ok := xidMap[xid]
		if !ok {
			logging.Error(ctx, "XID entry not found for XID", "xid", xid)
			return fmt.Errorf("XID entry not found for XID %d: %w", xid, grpcerr.ErrInternal)
		}
		exp.Acls = xidEntry.GetAcls()
	}
	return nil
}
