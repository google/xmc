// Copyright 2026 Google LLC
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

package pgdb

import (
	"context"
	"fmt"

	"xmanager_cloud/internal/grpcerr"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
)

// List normalizes params.Filter, builds and executes a paginated PostgreSQL SELECT statement,
// converts each scanned Row into Proto via rowToProto, and encodes the next page token via extractCursor.
func List[Row, Proto any](
	ctx context.Context,
	db DBTX,
	entity, pageToken string,
	params liststatement.ListParams,
	rowToProto func(Row) (Proto, error),
	extractCursor func(Proto) (map[string]any, error),
) ([]Proto, string, error) {
	stmt, err := buildListStatement(ctx, pageToken, params)
	if err != nil {
		return nil, "", err
	}
	rows, err := QueryRows[Row](ctx, db, stmt.SQL, entity, stmt.Args...)
	if err != nil {
		return nil, "", err
	}
	items, err := mapRowsToProtos(rows, rowToProto)
	if err != nil {
		return nil, "", err
	}
	return liststatement.ApplyPagination(items, params.PageSize, extractCursor)
}

func buildListStatement(ctx context.Context, pageToken string, params liststatement.ListParams) (*postgresdialect.Statement, error) {
	if params.QueryGen == nil || len(params.QueryGen.Tables()) == 0 {
		return nil, WrapStatusError("query generator or tables must not be empty", grpcerr.ErrFailedPrecondition, nil)
	}
	normalizedFilter, err := normalizer.Normalize(params.Filter, params.QueryGen.Tables()[0])
	if err != nil {
		return nil, fmt.Errorf("failed to normalize filter: %w", err)
	}
	params.Filter = normalizedFilter
	if err := params.DecodePageToken(pageToken); err != nil {
		return nil, err
	}
	stmt := postgresdialect.NewStatement("")
	if err := liststatement.ListStatement(ctx, stmt, params); err != nil {
		return nil, fmt.Errorf("failed to generate list statement: %w", err)
	}
	return stmt, nil
}

func mapRowsToProtos[Row, Proto any](rows []Row, rowToProto func(Row) (Proto, error)) ([]Proto, error) {
	items := make([]Proto, 0, len(rows))
	for _, row := range rows {
		item, err := rowToProto(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
