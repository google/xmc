// Copyright 2026 Google LLC
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

package artifact

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/logging"
	ls "github.com/google/xmc/internal/query_generator/liststatement"
	"github.com/google/xmc/internal/query_generator/normalizer"
	"github.com/google/xmc/internal/storage/spanner/data"
	"github.com/google/xmc/internal/storage/spanner/schema/artifactschema"
	"github.com/google/xmc/internal/storage/spanner/utils"
	"github.com/google/xmc/internal/validator"
)

// ListArtifacts returns a paginated list of Artifact protos based on the provided filter and order.
func (r *Repository) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error) {
	logging.Info(ctx, "List artifacts", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	var err error
	filter, ctx, err = buildParentFilter(ctx, parent, filter)
	if err != nil {
		return nil, "", err
	}

	normalizedFilter, err := normalizer.Normalize(filter, r.artifactsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.artifactsQueryGen,
		DefaultSelectFields:   artifactschema.ArtifactsSelectFields,
		DefaultFullTextFields: artifactschema.ArtifactsFullTextFields,
		ValidOrderByFields:    artifactschema.ArtifactsOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	// 3. Execute the query.
	expIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer expIter.Stop()

	// 4. Convert the rows to Artifact protos.
	var artifacts []*essmsgpb.Artifact
	if err := expIter.Do(func(row *spanner.Row) error {
		logging.Debug(ctx, "Converting row to Artifact proto", "row", row)
		exp, err := spannerdata.ArtifactRowToProto(row)
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
	artifacts, nextPageToken, err := ls.ApplyPagination(artifacts, pageSize, ExtractArtifactCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed artifacts", "count", len(artifacts), "nextPageToken", nextPageToken)

	// 6. Return the results.
	return artifacts, nextPageToken, nil
}

// ExtractArtifactCursor extracts pagination cursor values from an Artifact proto.
func ExtractArtifactCursor(artifact *essmsgpb.Artifact) (map[string]any, error) {
	aid, err := validator.ValidateArtifactName(artifact.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact name %q: %w", artifact.GetName(), err)
	}

	xid, wid, err := validator.ValidateArtifactParent(artifact.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact parent %q: %w", artifact.GetParent(), err)
	}

	cursor := map[string]any{
		"xid":              xid,
		"bit_reversed_xid": spannerutils.ReverseBits(xid),
		"work_unit_id":     nil,
		"artifact_id":      aid,
	}
	if wid != 0 {
		cursor["work_unit_id"] = wid
	}

	if artifact.CreateTime != nil {
		cursor["created"] = artifact.CreateTime.AsTime()
	}
	if artifact.UpdateTime != nil {
		cursor["updated"] = artifact.UpdateTime.AsTime()
	}
	var displayName string
	if artifact.GetUrl() != nil {
		displayName = artifact.GetUrl().GetDisplayName()
	}
	cursor["display_name"] = displayName

	var priority int64
	if artifact.GetPriority() != 0 {
		priority = int64(artifact.GetPriority())
	} else if artifact.GetUrl() != nil {
		priority = int64(artifact.GetUrl().GetPriority())
	}
	cursor["priority"] = priority

	return cursor, nil
}

func buildParentFilter(ctx context.Context, parent, filter string) (string, context.Context, error) {
	if parent == "" {
		return filter, ctx, nil
	}
	xid, wid, err := validator.ValidateArtifactParent(parent)
	if err != nil {
		return "", ctx, fmt.Errorf("failed to validate artifact parent name: %w", err)
	}
	ctx = logging.WithArtifactParent(ctx, parent)

	bitReversedXid := spannerutils.ReverseBits(xid)
	parentFilter := fmt.Sprintf("bit_reversed_xid = %d", bitReversedXid)
	if wid != 0 {
		parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
	}
	if filter == "" {
		return parentFilter, ctx, nil
	}
	return fmt.Sprintf("%s AND (%s)", parentFilter, filter), ctx, nil
}
