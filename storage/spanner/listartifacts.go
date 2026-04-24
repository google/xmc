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
package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/artifactschema"
	"xmanager_cloud/validator/validator"
)

// ListArtifacts returns a paginated list of Artifact protos based on the provided filter and order.
func (s *Storage) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*essmsgpb.Artifact, string, error) {
	logging.Info(ctx, "List artifacts", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	// 1. Ensure the results are filtered to the artifact's parent if present.
	if parent != "" {
		xid, wid, err := validator.ValidateArtifactParent(parent)
		if err != nil {
			return nil, "", fmt.Errorf("failed to validate artifact parent name: %w", err)
		}
		ctx = logging.WithArtifactParent(ctx, parent)

		// 1.1. Generate the filter for the parent.
		bitReversedXid := reverseBits(xid)
		parentFilter := fmt.Sprintf("bit_reversed_xid = %d", bitReversedXid)
		if wid != 0 {
			parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
		}
		if filter == "" {
			filter = parentFilter
		} else {
			filter = fmt.Sprintf("%s AND (%s)", parentFilter, filter)
		}
	}

	normalizedFilter, err := normalizer.Normalize(filter, s.artifactsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              s.artifactsQueryGen,
		DefaultSelectFields:   artifactschema.ArtifactsSelectFields,
		DefaultFullTextFields: artifactschema.ArtifactsFullTextFields,
		ValidOrderByFields:    artifactschema.ArtifactsOrderByFields,
	}

	isCursor, err := listParams.DecodePageToken(pageToken, defaultToCursorPagination)
	if err != nil {
		return nil, "", err
	}

	stmt, pageOffset, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	// 3. Execute the query.
	expIter := s.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer expIter.Stop()

	// 4. Convert the rows to Artifact protos.
	var artifacts []*essmsgpb.Artifact
	if err := expIter.Do(func(r *spanner.Row) error {
		logging.Debug(ctx, "Converting row to Artifact proto", "row", r)
		exp, err := spannerdata.ArtifactRowToProto(r)
		if err != nil {
			logging.Error(ctx, "failed to convert Artifact row to Artifact", "error", err)
			return err
		}
		artifacts = append(artifacts, exp)
		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read Artifact rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Artifact rows: %w", err)
	}

	// 5. If there are more results than the page size, return the next page token.
	artifacts, nextPageToken, err := ls.ApplyPagination(artifacts, pageSize, pageOffset, isCursor, extractArtifactCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed artifacts", "count", len(artifacts), "nextPageToken", nextPageToken)

	// 6. Return the results.
	return artifacts, nextPageToken, nil
}

func extractArtifactCursor(artifact *essmsgpb.Artifact) (map[string]any, error) {
	aid, err := validator.ValidateArtifactName(artifact.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact name %q: %w", artifact.GetName(), err)
	}

	xid, wid, err := validator.ValidateArtifactParent(artifact.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact parent %q: %w", artifact.GetParent(), err)
	}

	cursor := map[string]any{
		"xid":          xid,
		"work_unit_id": wid,
		"artifact_id":  aid,
	}

	if artifact.CreateTime != nil {
		cursor["created"] = artifact.CreateTime.AsTime()
	}
	if artifact.UpdateTime != nil {
		cursor["updated"] = artifact.UpdateTime.AsTime()
	}

	return cursor, nil
}
