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

package experiment

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/experimentschema"
	"xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

// ListExperiments returns a paginated list of Experiment protos based on the provided filter and order.
func (r *Repository) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error) {
	logging.Info(ctx, "List experiments", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)

	// 1. Normalize the filter.
	normalizedFilter, err := normalizer.Normalize(filter, r.experimentsQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 1.1. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              r.experimentsQueryGen,
		DefaultSelectFields:   experimentschema.ExperimentsSelectFields,
		DefaultFullTextFields: experimentschema.ExperimentsFullTextFields,
		ValidOrderByFields:    experimentschema.ExperimentsOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "stmt", stmt)

	// 2. Execute the query.
	expIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer expIter.Stop()

	// 3. Convert the rows to Experiment protos.
	var experiments []*essmsgpb.Experiment
	if err := expIter.Do(func(row *spanner.Row) error {
		exp, err := spannerdata.ExperimentRowToProto(row)
		if err != nil {
			return err
		}
		experiments = append(experiments, exp)
		return nil
	}); err != nil {
		logging.Error(ctx, "Failed to read Experiment rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Experiment rows: %w", err)
	}

	// 4. If there are more results than the page size, return the next page token.
	experiments, nextPageToken, err := ls.ApplyPagination(experiments, pageSize, ExtractExperimentCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed experiments", "count", len(experiments), "nextPageToken", nextPageToken)

	// 5. Return the results.
	return experiments, nextPageToken, nil
}

// ExtractExperimentCursor extracts pagination cursor values from an Experiment proto.
func ExtractExperimentCursor(exp *essmsgpb.Experiment) (map[string]any, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}

	cursor := map[string]any{
		"xid":              xid,
		"bit_reversed_xid": spannerutils.ReverseBits(xid),
		"completed":        nil,
	}

	if exp.CreateTime != nil {
		cursor["created"] = exp.CreateTime.AsTime()
	}
	if exp.UpdateTime != nil {
		cursor["updated"] = exp.UpdateTime.AsTime()
	}
	if exp.CompleteTime != nil {
		cursor["completed"] = exp.CompleteTime.AsTime()
	}

	return cursor, nil
}
