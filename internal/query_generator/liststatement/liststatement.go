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
// Package liststatement provides a function for generating a Spanner statement for a list query.
package liststatement

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	fepb "xmanager_cloud/api/filter/filter_expression_go_proto"
	aipfilter "xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/query_generator/querytools"

	"cloud.google.com/go/spanner"
	"encoding/base64"
	"encoding/json"
)

// ListParams contains the parameters for a list query.
type ListParams struct {
	PageSize int32
	Filter   string
	OrderBy  string

	// Query generator to use for generating the query.
	QueryGen querytools.QueryGenerator

	// Fields to select in the query.
	DefaultSelectFields []querytools.TableField

	// Full text fields to use for tokenized search.
	DefaultFullTextFields []safesql.TrustedSQLString

	// Fields that are valid for the ORDER BY clause.
	ValidOrderByFields []querytools.UserOrderedIndex

	// Indexed fields that should be used for the ORDER BY clause. These will always be included in
	// the ORDER BY clause.
	IndexOrderByFields []querytools.UserOrderedIndex

	// AfterCursor contains the field names and values for a cursor based pagination.
	AfterCursor map[string]any
}

// DecodePageToken decodes the page token and updates the ListParams with the result.
func (p *ListParams) DecodePageToken(pageToken string) error {
	afterCursor, err := DecodeAfterCursorPageToken(pageToken)
	if err != nil {
		return err
	}
	p.AfterCursor = afterCursor
	return nil
}

// ListStatement returns a Spanner statement for the given list parameters.
func ListStatement(ctx context.Context, params ListParams) (*spanner.Statement, error) {
	// 1. Parse the filter as an EBNF expression.
	exp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), params.Filter)
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter %s, %v: %w", params.Filter, err, grpcerr.ErrInvalidArgument)
	}

	logging.Info(ctx, "Parsed filter", "filter", exp)

	// 2. Convert the orderBy string to a list of ordered indices.
	orderedIndices, err := generateOrderBy(ctx, params.OrderBy, params.ValidOrderByFields)
	if err != nil {
		return nil, fmt.Errorf("failed to parse orderBy %s, %v: %w", params.OrderBy, err, grpcerr.ErrInvalidArgument)
	}

	// 3. Generate the query.
	pageSizeWithExtra := params.PageSize + 1

	var ptFields map[querytools.TableField]any
	if len(params.AfterCursor) > 0 {
		ptFields = make(map[querytools.TableField]any)
		for k, v := range params.AfterCursor {
			ptFields[querytools.TableField(k)] = v
		}
	}

	stmt, _, _, err := params.QueryGen.GenerateQuery(params.DefaultSelectFields, params.DefaultFullTextFields, nil, exp, orderedIndices, int(pageSizeWithExtra), 0, ptFields, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query: %w", err)
	}

	// 5. Ensure the query does not have duplicated select fields. This is a workaround; the query generator
	// should be updated to do this natively. The fundamental problem seems to be that that duplicate
	// selects are not considered a problem, but I think it's extremely bad practice to allow it, and
	// it causes the column parsing logic to be more complex in the row -> proto conversion logic.
	// However, this is not trivial, and our SQL logic is very simple, so we just do it here for now.
	stmt.SQL, err = deduplicateSelectColumns(stmt.SQL)
	if err != nil {
		return nil, fmt.Errorf("failed to deduplicate select columns: %w", err)
	}

	logging.Info(ctx, "Generated query SQL", "sql", stmt.SQL)
	logging.Info(ctx, "Generated query params", "params", stmt.Params)

	// 6. Return the results.
	return stmt, nil
}

// generateOrderBy splits the orderBy string by commas, trims whitespace, and returns an error for invalid segments.
func generateOrderBy(ctx context.Context, orderBy string, validFields []querytools.UserOrderedIndex) ([]querytools.UserOrderedIndex, error) {
	var orderedIndices []querytools.UserOrderedIndex

	if orderBy == "" {
		return nil, nil
	}

	// Split the orderBy string by commas.
	parts := strings.Split(orderBy, ",")
	var orderByParts []string
	for _, part := range parts {
		trimmedPart := strings.TrimSpace(part)
		if trimmedPart == "" {
			return nil, fmt.Errorf("invalid orderBy string %q: contains empty or whitespace-only segments", orderBy)
		}
		orderByParts = append(orderByParts, trimmedPart)
	}

	logging.Info(ctx, "orderByParts", "parts", orderByParts)

	for _, part := range orderByParts {
		// Split the orderBy segment by spaces.
		parts := strings.Fields(part)
		if len(parts) > 2 {
			return nil, fmt.Errorf("invalid orderBy string %q: segment %q contains more than one space", orderBy, part)
		}

		// Check that we either have "WORD" or "WORD desc". Technically, "WORD asc" is not valid, but
		// we allow it for simplicity.
		orderByDirection := querytools.Asc
		if len(parts) == 2 {
			direction := strings.ToLower(parts[1])
			if direction == "desc" {
				orderByDirection = querytools.Desc
			} else if direction != "asc" {
				return nil, fmt.Errorf("invalid orderBy string %q: segment %q contains invalid direction %q", orderBy, part, direction)
			}
		}

		// Check that the orderBy field is a valid field.
		var matchedField querytools.TableField
		found := false
		for _, field := range validFields {
			if strings.EqualFold(string(field.TableField), parts[0]) {
				matchedField = field.TableField
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid orderBy string %q: segment %q is not a valid field", orderBy, part)
		}

		orderedIndices = append(orderedIndices, querytools.UserOrderedIndex{TableField: matchedField, SortOrder: orderByDirection})
	}

	return orderedIndices, nil
}

// deduplicateSelectColumns takes a SQL query string and removes duplicate columns
// from the SELECT clause. It preserves the order of the first appearance of each column.
// Note: This is a simplified parser and works for basic "SELECT ... FROM ..." queries.
// It does not handle complex cases like subqueries in the SELECT clause.
func deduplicateSelectColumns(query string) (string, error) {
	// 1. Normalize the query to uppercase for case-insensitive matching of keywords.
	upperQuery := strings.ToUpper(query)

	// 2. Find the start of the SELECT columns and the end (start of FROM).
	selectIndex := strings.Index(upperQuery, "SELECT ")
	fromIndex := strings.Index(upperQuery, " FROM ")

	if selectIndex == -1 || fromIndex == -1 {
		return "", fmt.Errorf("query must contain SELECT and FROM clauses")
	}

	// Adjust selectIndex to point to the start of the column list.
	selectIndex += len("SELECT ")

	// 3. Extract the parts of the query.
	selectClause := query[:selectIndex]        // "SELECT "
	columnsStr := query[selectIndex:fromIndex] // "A, B, C, A"
	fromAndRestClause := query[fromIndex:]     // " FROM ..."

	// 4. Split the columns string into a slice.
	columns := strings.Split(columnsStr, ",")

	// 5. Use a map to track seen columns and a slice to store unique columns.
	// This preserves the original order of the columns.
	seen := make(map[string]bool)
	var uniqueColumns []string

	for _, col := range columns {
		// Trim whitespace from the column name.
		trimmedCol := strings.TrimSpace(col)

		// If we haven't seen this column before, add it to our unique list and mark it as seen.
		if _, ok := seen[trimmedCol]; !ok {
			seen[trimmedCol] = true
			uniqueColumns = append(uniqueColumns, trimmedCol)
		}
	}

	// 6. Join the unique columns back into a single string.
	uniqueColumnsStr := strings.Join(uniqueColumns, ", ")

	// 7. Reconstruct and return the final query.
	return selectClause + uniqueColumnsStr + fromAndRestClause, nil
}

// DecodeAfterCursorPageToken extracts the cursor map from a page token.
func DecodeAfterCursorPageToken(pageToken string) (map[string]any, error) {
	if pageToken == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(pageToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode pageToken %q, %w: %w", pageToken, err, grpcerr.ErrInvalidArgument)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var afterCursor map[string]any
	if err := dec.Decode(&afterCursor); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pageToken %q, %w: %w", pageToken, err, grpcerr.ErrInvalidArgument)
	}
	if len(afterCursor) == 0 {
		return nil, fmt.Errorf("decoded pageToken %q has empty cursor: %w", pageToken, grpcerr.ErrInvalidArgument)
	}
	normalizeCursorNumbers(afterCursor)
	return afterCursor, nil
}

func normalizeCursorNumbers(cursor map[string]any) {
	for k, v := range cursor {
		num, ok := v.(json.Number)
		if !ok {
			continue
		}
		if i, err := num.Int64(); err == nil {
			cursor[k] = i
		} else if f, err := num.Float64(); err == nil {
			cursor[k] = f
		}
	}
}

// EncodeAfterCursorPageToken encodes a cursor map into a page token.
func EncodeAfterCursorPageToken(afterCursor map[string]any) (string, error) {
	if len(afterCursor) == 0 {
		return "", nil
	}
	data, err := json.Marshal(afterCursor)
	if err != nil {
		return "", fmt.Errorf("failed to marshal afterCursor, %w: %w", err, grpcerr.ErrInternal)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// ApplyPagination handles the truncation of results and generation of the next page token for cursor-based pagination.
func ApplyPagination[T any](results []T, pageSize int32, extractCursor func(T) (map[string]any, error)) ([]T, string, error) {
	if pageSize <= 0 {
		return nil, "", fmt.Errorf("pageSize must be greater than zero")
	}
	if extractCursor == nil {
		return nil, "", fmt.Errorf("extractCursor function cannot be nil")
	}

	nextPageToken := ""
	if len(results) > int(pageSize) {
		results = results[:pageSize]
		lastItemCursor, err := extractCursor(results[int(pageSize)-1])
		if err != nil {
			return nil, "", err
		}
		token, err := EncodeAfterCursorPageToken(lastItemCursor)
		if err != nil {
			return nil, "", err
		}
		if token == "" {
			return nil, "", fmt.Errorf("failed to encode next page token: cursor is empty")
		}
		nextPageToken = token
	}
	return results, nextPageToken, nil
}
