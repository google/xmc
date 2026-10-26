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

package experiment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/experimentschema"
	"xmanager_cloud/internal/validator"
)

// experimentsQueryGen returns the query generator for the Experiments table view, built once per
// process from the static schema. The Spanner repository builds the same generator in its
// constructor; NewRepository cannot return an error, so it is built lazily instead.
var experimentsQueryGen = sync.OnceValues(func() (querytools.QueryGenerator, error) {
	return querytools.NewQueryGenerator(experimentschema.ExperimentTables, nil, nil, nil)
})

// ListExperiments returns a paginated list of Experiment protos based on the provided filter and
// order. Without an orderBy, results follow the default order declared by the Experiments table
// view (Xid ascending, the equivalent of the Spanner view's BitReversedXid order since Xid is an
// identity column). The staleness argument is accepted for interface parity and ignored:
// PostgreSQL has no bounded-staleness reads, so every list is a strong read.
func (r *Repository) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*pb.Experiment, string, error) {
	logging.Info(ctx, "List experiments", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)
	if r.db == nil {
		return nil, "", errNilPool
	}

	queryGen, err := experimentsQueryGen()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create experiments query generator: %w", err)
	}

	// 1. Normalize the filter.
	normalizedFilter, err := normalizer.Normalize(filter, queryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	// 1.1. Generate the list statement.
	listParams := ls.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              queryGen,
		DefaultSelectFields:   experimentschema.ExperimentsSelectFields,
		DefaultFullTextFields: experimentschema.ExperimentsFullTextFields,
		ValidOrderByFields:    experimentschema.ExperimentsOrderByFields,
	}

	if err = listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt := postgresdialect.NewStatement("")
	if err = ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	logging.Debug(ctx, "Generated list statement", "sql", stmt.SQL, "args", stmt.Args)

	// 2. Execute the query and convert the rows to Experiment protos.
	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		logging.Error(ctx, "Failed to query Experiment rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Experiment rows: %w", pgdb.TranslateError(err, "experiments"))
	}
	experiments, err := pgx.CollectRows(rows, scanExperiment)
	if err != nil {
		logging.Error(ctx, "Failed to read Experiment rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Experiment rows: %w", pgdb.TranslateError(err, "experiments"))
	}

	// 3. If there are more results than the page size, return the next page token.
	experiments, nextPageToken, err := ls.ApplyPagination(experiments, pageSize, ExtractExperimentCursor)
	if err != nil {
		return nil, "", err
	}

	logging.Info(ctx, "Listed experiments", "count", len(experiments), "nextPageToken", nextPageToken)

	// 4. Return the results.
	return experiments, nextPageToken, nil
}

// ExtractExperimentCursor extracts pagination cursor values from an Experiment proto. The cursor
// holds every field of the Experiments table view that a page may be ordered by, so a page token
// is valid for any orderBy; unlike the Spanner cursor it has no bit_reversed_xid, which the
// PostgreSQL view does not know.
func ExtractExperimentCursor(exp *pb.Experiment) (map[string]any, error) {
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate experiment name %q: %w", exp.GetName(), err)
	}

	cursor := map[string]any{
		"xid":       xid,
		"completed": nil,
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
