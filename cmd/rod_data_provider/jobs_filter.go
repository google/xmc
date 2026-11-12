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

package main

import (
	"fmt"
	"strconv"
	"strings"

	fpb "xmanager_cloud/api/filter"
	aipfilter "xmanager_cloud/api/filter/go/filter"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

// activeJobFilterStates restricts status filters to states present in WorkUnits.IsPendingOrRunningJob.
var activeJobFilterStates = map[string]essmsgpb.WorkUnitStatus_WorkUnitState{
	"RUNNING":      essmsgpb.WorkUnitStatus_RUNNING,
	"STARTING":     essmsgpb.WorkUnitStatus_STARTING,
	"INITIALIZING": essmsgpb.WorkUnitStatus_INITIALIZING,
	"SCHEDULING":   essmsgpb.WorkUnitStatus_SCHEDULING,
}

// parseJobsFilter parses an AIP-160 filter supporting author substring and active status equality joined by AND.
func parseJobsFilter(input string) (storage.ActiveWorkUnitFilter, error) {
	var result storage.ActiveWorkUnitFilter
	if strings.TrimSpace(input) == "" {
		return result, nil
	}
	expr, err := aipfilter.ParseExpressionWithOptions(fpb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), input)
	if err != nil {
		return result, err
	}
	if err := collectJobsFilter(expr, &result); err != nil {
		return storage.ActiveWorkUnitFilter{}, err
	}
	return result, nil
}

func collectJobsFilter(expr *fpb.Expression, result *storage.ActiveWorkUnitFilter) error {
	if expr.WhichExpression() == fpb.Expression_Conjunction_case {
		for _, child := range expr.GetConjunction().GetExpressions() {
			if err := collectJobsFilter(child, result); err != nil {
				return err
			}
		}
		return nil
	}
	restriction := expr.GetRestriction()
	if restriction == nil || restriction.GetLhs().WhichExpression() != fpb.Expression_Value_case {
		return fmt.Errorf("filter supports only author and status predicates joined with AND")
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
	return applyJobsFilterPredicate(result, restriction.GetLhs().GetValue(), restriction.GetComparator(), value)
}

func applyJobsFilterPredicate(result *storage.ActiveWorkUnitFilter, field string, op fpb.Restriction_Comparator, value string) error {
	switch field {
	case "author":
		if op != fpb.Restriction_EQUALS && op != fpb.Restriction_HAS {
			return fmt.Errorf("author supports only = or :")
		}
		if result.HasAuthor {
			return fmt.Errorf("author may appear only once")
		}
		author, err := strconv.Unquote(value)
		if err != nil {
			return fmt.Errorf("author must be a quoted string")
		}
		result.AuthorSubstring, result.HasAuthor = author, true
	case "status":
		if op != fpb.Restriction_EQUALS {
			return fmt.Errorf("status supports only =")
		}
		if result.Status != essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED {
			return fmt.Errorf("status may appear only once")
		}
		state, ok := activeJobFilterStates[value]
		if !ok {
			return fmt.Errorf("status must be RUNNING, STARTING, INITIALIZING, or SCHEDULING")
		}
		result.Status = state
	default:
		return fmt.Errorf("unsupported filter field %q", field)
	}
	return nil
}
