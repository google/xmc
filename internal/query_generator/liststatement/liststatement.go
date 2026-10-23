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
// Package liststatement provides a function for generating a SQL statement for a list query.
package liststatement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	fepb "xmanager_cloud/api/filter"
	aipfilter "xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	queryfilter "xmanager_cloud/internal/query_generator/filter"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
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

// ListStatement populates stmt with the generated SQL query and bound parameters for the given list parameters.
func ListStatement(ctx context.Context, stmt queryfilter.Statement, params ListParams) error {
	// 1. Parse the filter as an EBNF expression.
	exp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), params.Filter)
	if err != nil {
		return fmt.Errorf("failed to parse filter %s, %v: %w", params.Filter, err, grpcerr.ErrInvalidArgument)
	}

	logging.Info(ctx, "Parsed filter", "filter", exp)

	// 2. Convert the orderBy string to a list of ordered indices.
	orderedIndices, err := generateOrderBy(ctx, params.OrderBy, params.ValidOrderByFields)
	if err != nil {
		return fmt.Errorf("failed to parse orderBy %s, %v: %w", params.OrderBy, err, grpcerr.ErrInvalidArgument)
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

	_, _, err = params.QueryGen.GenerateQuery(stmt, params.DefaultSelectFields, params.DefaultFullTextFields, nil, exp, orderedIndices, int(pageSizeWithExtra), 0, ptFields, nil)
	if err != nil {
		return fmt.Errorf("failed to generate query: %w", err)
	}

	return nil
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
