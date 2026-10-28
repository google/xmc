// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
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

	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/artifactschema"
	"xmanager_cloud/internal/validator"
)

// ListArtifacts returns a paginated list of Artifact protos based on the provided parent, filter
// and order. The parent may be an experiment, which lists its experiment-level and work-unit-level
// artifacts alike, or a work unit; an empty parent lists across all experiments. Without an orderBy
// the TableView's default order applies: xid, work_unit_id (NULLs first) and artifact_id, all
// ascending. The staleness argument has no PostgreSQL equivalent and is ignored: every read is a
// strong read.
func (r *Repository) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*pb.Artifact, string, error) {
	logging.Info(ctx, "List artifacts", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	// 1. Scope the filter to the parent.
	var err error
	filter, ctx, err = buildParentFilter(ctx, parent, filter)
	if err != nil {
		return nil, "", err
	}

	queryGen, err := artifactsQueryGen()
	if err != nil {
		return nil, "", err
	}
	normalizedFilter, err := normalizer.Normalize(filter, queryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 2. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              queryGen,
		DefaultSelectFields:   artifactschema.ArtifactsSelectFields,
		DefaultFullTextFields: artifactschema.ArtifactsFullTextFields,
		ValidOrderByFields:    artifactschema.ArtifactsOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt := postgresdialect.NewStatement("")
	if err := ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "sql", stmt.SQL, "args", stmt.Args)

	// 3. Execute the query.
	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		logging.Error(ctx, "failed to query Artifact rows", "error", err)
		return nil, "", pgdb.TranslateError(err, entityName)
	}
	defer rows.Close()

	// 4. Convert the rows to Artifact protos.
	var artifacts []*pb.Artifact
	columnCount := len(rows.FieldDescriptions())
	for rows.Next() {
		artifact, err := scanArtifact(rows, columnCount)
		if err != nil {
			logging.Error(ctx, "failed to convert Artifact row to Artifact", "error", err)
			return nil, "", fmt.Errorf("failed to read Artifact rows: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		logging.Error(ctx, "failed to read Artifact rows", "error", err)
		return nil, "", pgdb.TranslateError(err, entityName)
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

// ExtractArtifactCursor extracts pagination cursor values from an Artifact proto. The cursor holds
// every field that may appear in an ORDER BY clause: the default ordering fields xid, work_unit_id
// (nil for experiment-level artifacts) and artifact_id, and the user-orderable created, updated,
// display_name and priority. Unlike the Spanner cursor it carries no bit_reversed_xid, which the
// PostgreSQL schema does not have.
func ExtractArtifactCursor(artifact *pb.Artifact) (map[string]any, error) {
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
		"work_unit_id": nil,
		"artifact_id":  aid,
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

// buildParentFilter prefixes filter with the restriction that scopes the query to parent: the
// experiment's xid and, for a work unit parent, its work_unit_id. An experiment parent therefore
// matches the experiment's work-unit-level artifacts as well. An empty parent leaves filter as is.
func buildParentFilter(ctx context.Context, parent, filter string) (string, context.Context, error) {
	if parent == "" {
		return filter, ctx, nil
	}
	xid, wid, err := validator.ValidateArtifactParent(parent)
	if err != nil {
		return "", ctx, fmt.Errorf("failed to validate artifact parent name: %w", err)
	}
	ctx = logging.WithArtifactParent(ctx, parent)

	parentFilter := fmt.Sprintf("xid = %d", xid)
	if wid != 0 {
		parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
	}
	if filter == "" {
		return parentFilter, ctx, nil
	}
	return fmt.Sprintf("%s AND (%s)", parentFilter, filter), ctx, nil
}
