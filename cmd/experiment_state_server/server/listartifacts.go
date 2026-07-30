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

	"golang/protobuf/v2/proto/proto"
	"task/go/status"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/validator/validator"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

const (
	// defaultArtifactPageSize is the number of results returned if the client
	// does not specify a page_size.
	defaultArtifactPageSize = 10
	// artifactPageSize is the maximum page size for ListArtifacts requests.
	// page sizes.
	maxArtifactPageSize = 1000
	// artifactSearchStaleness is the staleness for SearchArtifacts requests.
	artifactSearchStaleness = 15 * time.Second
)

func (s *Server) ListArtifacts(ctx context.Context, req *apipb.ListArtifactsRequest) (*apipb.ListArtifactsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Fetch artifacts", "request", req)
	resp, err := s.listArtifactsWithStaleness(ctx, req, 0*time.Second)
	logging.Info(ctx, "Fetched artifacts", "response", resp)
	return resp, err
}

func (s *Server) SearchArtifacts(ctx context.Context, req *apipb.ListArtifactsRequest) (*apipb.ListArtifactsResponse, error) {
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Search artifacts", "request", req)
	resp, err := s.listArtifactsWithStaleness(ctx, req, artifactSearchStaleness)
	logging.Info(ctx, "Searched artifacts", "response", resp)
	return resp, err
}

func (s *Server) listArtifactsWithStaleness(ctx context.Context, req *apipb.ListArtifactsRequest, staleness time.Duration) (*apipb.ListArtifactsResponse, error) {
	// 1. Authenticate the user.
	_, _, err := s.auth.AuthenticateUserFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Validate the request payload.
	if req.GetPageSize() == 0 {
		logging.Warn(ctx, "Page size not specified, using default", "page_size", defaultArtifactPageSize)
		req.PageSize = defaultArtifactPageSize
	}
	if err := validator.ValidateAIPCompliantListWithMaxPageSize(req.GetPageSize(), req.GetFilter(), req.GetOrderBy(), req.GetPageToken(), maxArtifactPageSize); err != nil {
		logging.Warn(ctx, "Invalid ListArtifactsRequest", "error", err)
		return nil, fmt.Errorf("invalid ListArtifactsRequest: %w", status.ErrInvalidArgument)
	}

	// 3. Get the list of Artifacts from the storage layer.
	artifacts, nextPageToken, err := s.storage.ListArtifacts(ctx, req.GetParent(), req.GetPageSize(), req.GetPageToken(), req.GetFilter(), req.GetOrderBy(), staleness)
	if err != nil {
		statusErr, ok := status.FromError(err)
		if !ok {
			logging.Error(ctx, "Storage layer returned a non-gRPC error for listing Artifacts", "error", err)
			return nil, fmt.Errorf("an internal error occurred while retrieving the Artifacts: %w", status.ErrInternal)
		}

		if statusErr.Code == status.InvalidArgument {
			logging.Error(ctx, "Storage layer returned an invalid argument error for listing Artifacts", "error", err)
			return nil, fmt.Errorf("invalid ListArtifactsRequest: %w", err)
		}

		logging.Error(ctx, "Storage layer returned a gRPC error for listing artifacts", "error", err)
		return nil, fmt.Errorf("an internal error occurred while retrieving the artifacts: %w", status.ErrInternal)
	}

	// 4. Populate ACLs from XID service.
	if err := s.fetchACLsForArtifacts(ctx, artifacts, req.GetPageSize()); err != nil {
		return nil, err
	}

	// 5. Enrich artifact output fields (e.g. ManagedXprof with the MLRun URL).
	for _, artifact := range artifacts {
		s.enrichArtifactOutputFields(ctx, artifact)
	}

	// 6. Return the successful response.
	return &apipb.ListArtifactsResponse{
		Artifacts:     artifacts,
		NextPageToken: nextPageToken,
	}, nil
}

func (s *Server) fetchACLsForArtifacts(ctx context.Context, artifacts []*essmsgpb.Artifact, pageSize int32) error {
	if len(artifacts) == 0 {
		return nil
	}
	// Generate a filter that will return the same artifacts, but in the form of XIDs.
	var xidFilter []string
	for _, artifact := range artifacts {
		xid, err := validator.ValidateArtifactName(artifact.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate artifact name", "error", err)
			return fmt.Errorf("failed to validate artifact name: %w", status.ErrInternal)
		}
		xidFilter = append(xidFilter, fmt.Sprintf("bit_reversed_xid=%d", data.ReverseBits(xid)))
	}

	// Call the XID service to get the XID entries.
	xidListResponse, err := s.xidClient.ListXidEntries(propagateIAPJWT(ctx), &xidapipb.ListXidEntriesRequest{
		PageSize: proto.Int32(pageSize),
		// page sizes.
		Filter: proto.String(strings.Join(xidFilter, " OR ")),
	})
	if err != nil {
		logging.Error(ctx, "Failed to list XIDs", "error", err)
		return fmt.Errorf("failed to list XIDs: %w", status.ErrInternal)
	}
	xidEntries := xidListResponse.GetXidEntries()

	// Build a map of XID to XID entry for quick lookup.
	xidMap := make(map[int64]*xidpb.XidEntry)
	for _, xidEntry := range xidEntries {
		xid, err := validator.ValidateXIDName(xidEntry.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate XID name", "name", xidEntry.GetName(), "error", err)
			return fmt.Errorf("failed to validate XID name: %w", status.ErrInternal)
		}
		xidMap[xid] = xidEntry
	}

	// Update the artifacts with the XID entries.
	for _, artifact := range artifacts {
		xid, err := validator.ValidateArtifactName(artifact.GetName())
		if err != nil {
			logging.Error(ctx, "Failed to validate artifact name", "error", err)
			return fmt.Errorf("failed to validate artifact name: %w", status.ErrInternal)
		}
		xidEntry, ok := xidMap[xid]
		if !ok {
			logging.Error(ctx, "XID entry not found for XID", "xid", xid)
			return fmt.Errorf("XID entry not found for XID %d: %w", xid, status.ErrInternal)
		}
		artifact.Acls = xidEntry.GetAcls()
	}
	return nil
}
