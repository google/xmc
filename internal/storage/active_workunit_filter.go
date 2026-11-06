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

package storage

import (
	"fmt"
	"strconv"
	"strings"
	fpb "xmanager_cloud/api/filter"
	aipfilter "xmanager_cloud/api/filter/go/filter"
)

// ActiveWorkUnitQuery holds validated display ordering and filtering before pagination.
type ActiveWorkUnitQuery struct {
	OrderBy string
	Filter  ActiveWorkUnitFilter
}

// ActiveWorkUnitFilter contains the supported exact-match predicates.
type ActiveWorkUnitFilter struct {
	Author    string
	HasAuthor bool
	Status    string
}

// ParseActiveWorkUnitFilter accepts author and status equalities joined with AND.
func ParseActiveWorkUnitFilter(input string) (ActiveWorkUnitFilter, error) {
	result := ActiveWorkUnitFilter{}
	if strings.TrimSpace(input) == "" {
		return result, nil
	}
	expr, err := aipfilter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true, EnableSimpleQuotedStrings: true, EnforceValidStrings: true,
	}.Build(), input)
	if err != nil {
		return result, fmt.Errorf("invalid filter: %w", err)
	}
	err = collectActiveWorkUnitFilter(expr, &result)
	return result, err
}

func collectActiveWorkUnitFilter(expr *fpb.Expression, result *ActiveWorkUnitFilter) error {
	if expr.WhichExpression() == fpb.Expression_Conjunction_case {
		for _, child := range expr.GetConjunction().GetExpressions() {
			if err := collectActiveWorkUnitFilter(child, result); err != nil {
				return err
			}
		}
		return nil
	}
	restriction := expr.GetRestriction()
	if restriction == nil || restriction.GetComparator() != fpb.Restriction_EQUALS || restriction.GetLhs().WhichExpression() != fpb.Expression_Value_case {
		return fmt.Errorf("filter supports only author and status equalities joined with AND")
	}
	rhs := restriction.GetRhs()
	value := rhs.GetValue()
	switch rhs.WhichExpression() {
	case fpb.Expression_Value_case:
	case fpb.Expression_Phrase_case:
		value = strconv.Quote(strings.Join(rhs.GetPhrase().GetValues(), " "))
	default:
		return fmt.Errorf("filter values must be literals")
	}
	return setActiveWorkUnitFilter(result, restriction.GetLhs().GetValue(), value)
}

func setActiveWorkUnitFilter(result *ActiveWorkUnitFilter, field, value string) error {
	switch field {
	case "author":
		if result.HasAuthor {
			return fmt.Errorf("author may appear only once")
		}
		author, err := strconv.Unquote(value)
		if err != nil {
			return fmt.Errorf("author must be a quoted string")
		}
		result.Author, result.HasAuthor = author, true
	case "status":
		if result.Status != "" {
			return fmt.Errorf("status may appear only once")
		}
		switch value {
		case "RUNNING", "STARTING", "INITIALIZING", "SCHEDULING":
		default:
			return fmt.Errorf("status must be RUNNING, STARTING, INITIALIZING, or SCHEDULING")
		}
		result.Status = value
	default:
		return fmt.Errorf("unsupported filter field %q", field)
	}
	return nil
}

// ParseActiveWorkUnitQuery validates the API query before it reaches storage.
func ParseActiveWorkUnitQuery(orderBy, filter string) (ActiveWorkUnitQuery, error) {
	ordering, err := ActiveWorkUnitOrderBy(orderBy)
	if err != nil {
		return ActiveWorkUnitQuery{}, err
	}
	parsed, err := ParseActiveWorkUnitFilter(filter)
	return ActiveWorkUnitQuery{OrderBy: ordering, Filter: parsed}, err
}
