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

package queryfilterbase

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	fpb "api/filter/filter_expression_go_proto"
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
)

var IntConst = filtervisitor.CustomType("INT_CONST")

type ExpOptions struct {
	UseFullTextSearch         bool
	UseSubstringTextSearch    bool
	UseHasOperatorAsContains  bool
}

type QueryFilterVisitor interface {
	filtervisitor.FilterVisitor

	GetOptimizeDisjunction() bool
	SetLastType(t filtervisitor.PrimType) // wait, the package namespace is filtervisitor!
	GetLastType() filtervisitor.PrimType
	SetIsRHS(isRHS bool)
	GetIsRHS() bool
	SetIsHAS(isHAS bool)
	GetIsHAS() bool
	GetSQL() string
	SetSQL(s string)
	HandleMember(path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error)
	HandleFunction(functionName string, args []*fpb.Expression) (bool, filtervisitor.PrimType, error)
	TranslateRestriction(r *fpb.Restriction) (bool, *fpb.Restriction)
	HandleDisjunction(value []string) (bool, safesql.TrustedSQLString, error)
	GetTextSearchColumns() ([]safesql.TrustedSQLString, error)
	HandleTextSearch(col safesql.TrustedSQLString, val string) (safesql.TrustedSQLString, error)
	GetExpOptions() *ExpOptions

	AppendBindingTimestamp(t time.Time)
	AppendBindingString(s string)
	AppendBindingStringArray(s []string)
	AppendBindingByteArray(b []byte)
	AppendBindingInt64(i int64)
	AppendBindingUInt64(i uint64)
	AppendBindingFloat64(f float64)
	AppendBindingBool(b bool)
	AppendBindingEnum(e string)
	AppendTrustedSQL(sql safesql.TrustedSQLString)
}


func AppendFilters(qfv QueryFilterVisitor, e *fpb.Expression) error {
	_, err := filtervisitor.Visit(qfv, e)
	return err
}

func ConjunctionExp(qfv QueryFilterVisitor, el *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	exprs := el.GetExpressions()
	if len(exprs) == 0 {
		return filtervisitor.Invalid, errors.New("empty conjunction list")
	}
	qfv.AppendTrustedSQL(safesql.New("("))
	for i, expr := range exprs {
		if i > 0 {
			qfv.AppendTrustedSQL(safesql.New(" AND "))
		}
		if _, err := filtervisitor.Visit(qfv, expr); err != nil {
			return filtervisitor.Invalid, err
		}
	}
	qfv.AppendTrustedSQL(safesql.New(")"))
	return filtervisitor.Invalid, nil
}

func DisjunctionExp(qfv QueryFilterVisitor, el *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	exprs := el.GetExpressions()
	if len(exprs) == 0 {
		return filtervisitor.Invalid, errors.New("empty disjunction list")
	}

	// Group terms by LHS path for IN list optimization
	type groupEntry struct {
		path   []string
		exprs  []*fpb.Expression
		values []string
	}
	groups := make(map[string]*groupEntry)

	for _, expr := range exprs {
		isOptimizable := false
		var path []string
		var literalVal string

		if expr.WhichExpression() == fpb.Expression_Restriction_case {
			r := expr.GetRestriction()
			if r.GetComparator() == fpb.Restriction_EQUALS {
				lhs := r.GetLhs()
				rhs := r.GetRhs()

				// Extract LHS path
				if lhs.WhichExpression() == fpb.Expression_Member_case {
					for _, component := range lhs.GetMember().GetExpressions() {
						if component.WhichExpression() == fpb.Expression_Value_case {
							path = append(path, component.GetValue())
						}
					}
				} else if lhs.WhichExpression() == fpb.Expression_Value_case {
					path = []string{lhs.GetValue()}
				}

				// Extract RHS literal value
				if len(path) > 0 && rhs != nil {
					if rhs.WhichExpression() == fpb.Expression_Phrase_case {
						literalVal = strings.Join(rhs.GetPhrase().GetValues(), " ")
					} else if rhs.WhichExpression() == fpb.Expression_Value_case {
						val := rhs.GetValue()
						if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
							val = val[1 : len(val)-1]
						}
						literalVal = val
					}
				}

				if len(path) > 0 && literalVal != "" && qfv.GetOptimizeDisjunction() {
					if optimizable, _, err := qfv.HandleDisjunction(path); err == nil && optimizable {
						isOptimizable = true
					}
				}
			}
		}

		if isOptimizable {
			key := strings.Join(path, ".")
			if _, ok := groups[key]; !ok {
				groups[key] = &groupEntry{path: path}
			}
			groups[key].exprs = append(groups[key].exprs, expr)
			groups[key].values = append(groups[key].values, literalVal)
		}
	}

	// Print the final combined OR list in the exact original order!
	qfv.AppendTrustedSQL(safesql.New("("))
	hasFirst := false
	processedGroups := make(map[string]bool)

	for _, expr := range exprs {
		// Detect if this expression belongs to an optimized group (size >= 3)
		isOptimized := false
		var key string

		if expr.WhichExpression() == fpb.Expression_Restriction_case {
			r := expr.GetRestriction()
			if r.GetComparator() == fpb.Restriction_EQUALS {
				lhs := r.GetLhs()
				var path []string
				if lhs.WhichExpression() == fpb.Expression_Member_case {
					for _, component := range lhs.GetMember().GetExpressions() {
						if component.WhichExpression() == fpb.Expression_Value_case {
							path = append(path, component.GetValue())
						}
					}
				} else if lhs.WhichExpression() == fpb.Expression_Value_case {
					path = []string{lhs.GetValue()}
				}
				if len(path) > 0 {
					key = strings.Join(path, ".")
					if g, ok := groups[key]; ok && len(g.exprs) >= 2 {
						isOptimized = true
					}
				}
			}
		}

		if isOptimized {
			if !processedGroups[key] {
				processedGroups[key] = true
				g := groups[key]
				_, colSQL, _ := qfv.HandleDisjunction(g.path)
				oldSQL := qfv.GetSQL()
				qfv.SetSQL("")
				qfv.AppendBindingStringArray(g.values)
				paramSQL := qfv.GetSQL()
				qfv.SetSQL(oldSQL)

				if hasFirst {
					qfv.AppendTrustedSQL(safesql.New(" OR "))
				}
				qfv.AppendTrustedSQL(safesql.New("(" + colSQL.String() + " IN UNNEST (" + paramSQL + "))"))
				hasFirst = true
			}
			// Subsequent terms in the optimized group are skipped!
		} else {
			if hasFirst {
				qfv.AppendTrustedSQL(safesql.New(" OR "))
			}
			if _, err := filtervisitor.Visit(qfv, expr); err != nil {
				return filtervisitor.Invalid, err
			}
			hasFirst = true
		}
	}
	qfv.AppendTrustedSQL(safesql.New(")"))
	return filtervisitor.Invalid, nil
}

func NegationExp(qfv QueryFilterVisitor, e *fpb.Expression) (filtervisitor.PrimType, error) {
	oldSQL := qfv.GetSQL()
	qfv.SetSQL("")
	t, err := filtervisitor.Visit(qfv, e)
	if err != nil {
		qfv.SetSQL(oldSQL)
		return filtervisitor.Invalid, err
	}
	childSQL := qfv.GetSQL()
	qfv.SetSQL(oldSQL)

	qfv.AppendTrustedSQL(safesql.New("NOT " + childSQL))
	return t, nil
}

func RestrictionExp(qfv QueryFilterVisitor, r *fpb.Restriction) (filtervisitor.PrimType, error) {
	if ok, rewritten := qfv.TranslateRestriction(r); ok && rewritten != r {
		return filtervisitor.Visit(qfv, &fpb.Expression{
			Expression: &fpb.Expression_Restriction{
				Restriction: rewritten,
			},
		})
	}

	lhs := r.GetLhs()
	rhs := r.GetRhs()
	op := r.GetComparator()

	if rhs != nil {
		w := rhs.WhichExpression()
		if w == fpb.Expression_Conjunction_case || w == fpb.Expression_Disjunction_case || w == fpb.Expression_Negation_case {
			return filtervisitor.Invalid, fmt.Errorf("RHS cannot contain logical operators (AND/OR/NOT)")
		}
	}

	if op == fpb.Restriction_GLOBAL {
		if qfv.GetExpOptions().UseFullTextSearch {
			return handleTextSearch(qfv, rhs)
		}
		rhsType, err := filtervisitor.Visit(qfv, rhs)
		if err != nil {
			return filtervisitor.Invalid, err
		}
		if !qfv.GetIsRHS() && !rhsType.Equals(filtervisitor.Boolean) {
			return filtervisitor.Invalid, fmt.Errorf("unary filter field must be boolean, got %v", rhsType.Name)
		}
		if qfv.GetIsRHS() {
			return rhsType, nil
		}
		return filtervisitor.Boolean, nil
	}

	// Special full text search HAS case
	if op == fpb.Restriction_HAS && isLhsTextSearch(lhs) {
		return handleTextSearch(qfv, rhs)
	}

	if op == fpb.Restriction_HAS {
		qfv.SetIsHAS(true)
	}
	defer func() {
		if op == fpb.Restriction_HAS {
			qfv.SetIsHAS(false)
		}
	}()

	// Capture LHS!
	oldSQL := qfv.GetSQL()
	qfv.SetSQL("")
	lhsType, err := filtervisitor.Visit(qfv, lhs)
	if err != nil {
		qfv.SetSQL(oldSQL)
		return filtervisitor.Invalid, err
	}
	lhsSQL := qfv.GetSQL()
	qfv.SetSQL(oldSQL)

	// Propagate lastType and capture RHS!
	qfv.SetLastType(lhsType)
	qfv.SetIsRHS(true)
	qfv.SetSQL("")
	rhsType, err := filtervisitor.Visit(qfv, rhs)
	if err != nil {
		qfv.SetSQL(oldSQL)
		return filtervisitor.Invalid, err
	}
	rhsSQL := qfv.GetSQL()
	qfv.SetSQL(oldSQL)
	qfv.SetIsRHS(false)

	// Enforce strict/legacy-fallback type compatibility!
	isCompatible := lhsType.Compatible(rhsType)

	if op == fpb.Restriction_HAS {
		isLhsArray := false
		var lhsArrayElemType filtervisitor.PrimType
		var path []string
		if lhs.WhichExpression() == fpb.Expression_Member_case {
			for _, expr := range lhs.GetMember().GetExpressions() {
				if expr.WhichExpression() == fpb.Expression_Value_case {
					path = append(path, expr.GetValue())
				}
			}
		} else if lhs.WhichExpression() == fpb.Expression_Value_case {
			path = []string{lhs.GetValue()}
		}

		if len(path) > 0 {
			if _, t, err := qfv.HandleMember(path); err == nil && t.Name == filtervisitor.Array {
				isLhsArray = true
				if t.SubType != nil {
					lhsArrayElemType = *t.SubType
				}
			}
		}

		if isLhsArray {
			// Array HAS type safety verification!
			isCompatible = lhsArrayElemType.Compatible(rhsType)
			if !isCompatible {
				isNumericOrBool := func(n string) bool { return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" || n == "BOOLEAN" }
				isStringy := func(n string) bool { return n == "STRING" || n == "ENUM" || n == "STR_CONST" }
				if isNumericOrBool(lhsArrayElemType.Name) && isStringy(rhsType.Name) {
					isCompatible = true
				}
			}
			if !isCompatible {
				return filtervisitor.Invalid, fmt.Errorf("type mismatch in array HAS: array element is %v, RHS is %v", lhsArrayElemType.Name, rhsType.Name)
			}

			// ( @param IN col )
			qfv.AppendTrustedSQL(safesql.New("(" + rhsSQL + " IN " + lhsSQL + ")"))
			return filtervisitor.Invalid, nil
		}

		// String/Bytes HAS type safety verification!
		if !lhsType.Equals(filtervisitor.String) && !lhsType.Equals(filtervisitor.Bytes) {
			return filtervisitor.Invalid, fmt.Errorf("HAS operator is not supported for type %v", lhsType.Name)
		}
		if !qfv.GetExpOptions().UseHasOperatorAsContains {
			return filtervisitor.Invalid, fmt.Errorf("HAS operator is not supported for string/bytes fields unless has contains option is enabled")
		}
		if !lhsType.Compatible(rhsType) {
			return filtervisitor.Invalid, fmt.Errorf("type mismatch in HAS: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
		}

		// Substring matching HAS operator!
		if qfv.GetExpOptions().UseHasOperatorAsContains {
			qfv.AppendTrustedSQL(safesql.New("(STRPOS(" + lhsSQL + " , " + rhsSQL + ") > 0)"))
			return filtervisitor.Invalid, nil
		}

		// (REGEXP_CONTAINS(col , @param))
		qfv.AppendTrustedSQL(safesql.New("(REGEXP_CONTAINS(" + lhsSQL + " , " + rhsSQL + "))"))
		return filtervisitor.Invalid, nil
	}

	if op == fpb.Restriction_REGEXP_MATCH || op == fpb.Restriction_REGEXP_NOT_MATCH {
		if !lhsType.Equals(filtervisitor.String) && !lhsType.Equals(filtervisitor.Bytes) {
			return filtervisitor.Invalid, fmt.Errorf("regexp matching operator is not supported for type %v", lhsType.Name)
		}
		if !lhsType.Compatible(rhsType) {
			return filtervisitor.Invalid, fmt.Errorf("type mismatch in regexp match: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
		}

		if op == fpb.Restriction_REGEXP_MATCH {
			qfv.AppendTrustedSQL(safesql.New("(REGEXP_CONTAINS(" + lhsSQL + " , " + rhsSQL + "))"))
		} else {
			qfv.AppendTrustedSQL(safesql.New("(NOT (REGEXP_CONTAINS(" + lhsSQL + " , " + rhsSQL + ")))"))
		}
		return filtervisitor.Invalid, nil
	}

	// Standard comparison type safety verification!
	if !isCompatible && (op == fpb.Restriction_EQUALS || op == fpb.Restriction_NOT_EQUALS) {
		isNumericOrBool := func(n string) bool { return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" || n == "BOOLEAN" || n == "TIMESTAMP" || n == "DATE" || n == "PROTO_TIMESTAMP" }
		isStringy := func(n string) bool { return n == "STRING" || n == "ENUM" || n == "STR_CONST" }
		if isNumericOrBool(lhsType.Name) && isStringy(rhsType.Name) {
			isCompatible = true
		}
	}
	if !isCompatible {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
	}

	// Format comparison operators
	opSQL := ""
	switch op {
	case fpb.Restriction_EQUALS:
		opSQL = " = "
	case fpb.Restriction_NOT_EQUALS:
		opSQL = " != "
	case fpb.Restriction_LESS_THAN:
		opSQL = " < "
	case fpb.Restriction_LESS_EQUALS:
		opSQL = " <= "
	case fpb.Restriction_GREATER_THAN:
		opSQL = " > "
	case fpb.Restriction_GREATER_EQUALS:
		opSQL = " >= "
	default:
		opSQL = " = "
	}

	qfv.AppendTrustedSQL(safesql.New("(" + lhsSQL + opSQL + rhsSQL + ")"))
	return filtervisitor.Invalid, nil
}

func MemberExp(qfv QueryFilterVisitor, el *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	exprs := el.GetExpressions()
	var path []string
	for _, expr := range exprs {
		if expr.WhichExpression() == fpb.Expression_Value_case {
			path = append(path, expr.GetValue())
		}
	}

	// Check RHS enum symbol coercion context!
	if qfv.GetIsRHS() && (qfv.GetLastType().Equals(filtervisitor.Enum) || qfv.GetLastType().Equals(filtervisitor.StrConst)) {
		pathStr := strings.Join(path, ".")
		qfv.AppendBindingEnum(pathStr)
		return qfv.GetLastType(), nil
	}

	sql, t, err := qfv.HandleMember(path)
	if err != nil && qfv.GetIsRHS() {
		joined := strings.Join(path, ".")
		qfv.AppendBindingString(joined)
		return filtervisitor.String, nil
	}
	if err != nil {
		return filtervisitor.Invalid, err
	}
	qfv.AppendTrustedSQL(sql)
	return t, nil
}

func FunctionExp(qfv QueryFilterVisitor, f *fpb.Function) (filtervisitor.PrimType, error) {
	funcName := ""
	if f.GetFunction().WhichExpression() == fpb.Expression_Value_case {
		funcName = f.GetFunction().GetValue()
	}
	if ok, t, err := qfv.HandleFunction(funcName, f.GetArgs()); ok {
		return t, err
	}
	return filtervisitor.Invalid, fmt.Errorf("unsupported function call %q", funcName)
}

func ValueExp(qfv QueryFilterVisitor, val string) (filtervisitor.PrimType, error) {
	hasQuotes := (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'"))

	if !qfv.GetIsRHS() && !hasQuotes {
		sql, t, err := qfv.HandleMember([]string{val})
		if err != nil {
			return filtervisitor.Invalid, err
		}
		qfv.AppendTrustedSQL(sql)
		return t, nil
	}
	lastType := qfv.GetLastType()
	if qfv.GetIsRHS() && !hasQuotes {
		isEnumOrStrConst := lastType.Equals(filtervisitor.Enum) || lastType.Equals(filtervisitor.StrConst)
		if !isEnumOrStrConst {
			sql, t, err := qfv.HandleMember([]string{val})
			if err == nil {
				qfv.AppendTrustedSQL(sql)
				return t, nil
			}
		}
	}


	// Rule B: Flag unquoted numeric/boolean literals on stringy fields as mismatched types!
	if !hasQuotes && !qfv.GetIsHAS() && (lastType.Equals(filtervisitor.String) || lastType.Equals(filtervisitor.Bytes) || lastType.Equals(filtervisitor.Enum) || lastType.Equals(filtervisitor.StrConst)) {
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			return filtervisitor.Float, nil // Returns numeric to trigger type mismatch!
		}
		if val == "true" || val == "false" {
			return filtervisitor.Boolean, nil // Returns boolean to trigger type mismatch!
		}
	}

	// Rule A: Quoted literals on numeric/boolean LHS go straight to fallbackString!
	if hasQuotes {
		isNumericOrBool := func(n string) bool {
			return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" || n == "BOOLEAN"
		}
		if isNumericOrBool(lastType.Name) {
			goto fallbackString
		}
	}

	// Typed Binding Parameter Dispatcher based on LHS type
	if (lastType.Equals(filtervisitor.Integer) || lastType.Equals(filtervisitor.Unsigned)) && !hasQuotes {
		if lastType.Equals(filtervisitor.Unsigned) {
			if i, err := strconv.ParseInt(val, 10, 64); err == nil && i < 0 {
				return filtervisitor.Invalid, fmt.Errorf("negative value %v is out of bounds for unsigned field", val)
			}
		}

		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			// Fallback to try unsigned
			u, errOpt := strconv.ParseUint(val, 10, 64)
			if errOpt == nil {
				qfv.AppendBindingUInt64(u)
				return filtervisitor.Unsigned, nil
			}
			goto fallbackString
		}
		qfv.AppendBindingInt64(i)
		return filtervisitor.Integer, nil
	}

	if lastType.Equals(filtervisitor.Float) {
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			goto fallbackString
		}
		qfv.AppendBindingFloat64(f)
		return filtervisitor.Float, nil
	}

	if lastType.Equals(filtervisitor.Boolean) {
		b, err := strconv.ParseBool(val)
		if err != nil {
			goto fallbackString
		}
		qfv.AppendBindingBool(b)
		return filtervisitor.Boolean, nil
	}

	if lastType.Equals(filtervisitor.Timestamp) || lastType.Equals(filtervisitor.ProtoTimestamp) {
		// Strip quotes if present
		cleanVal := val
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			cleanVal = val[1 : len(val)-1]
		}
		// Try parsing ISO8601/RFC3339 timestamp formats
		t, err := time.Parse(time.RFC3339, cleanVal)
		if err != nil {
			// Try alternate date formats
			t, err = time.Parse("2006-01-02", cleanVal)
			if err != nil {
				goto fallbackString
			}
		}
		qfv.AppendBindingTimestamp(t)
		return lastType, nil
	}

	if lastType.Equals(filtervisitor.Bytes) {
		cleanVal := val
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			cleanVal = val[1 : len(val)-1]
		}
		qfv.AppendBindingByteArray([]byte(cleanVal))
		return filtervisitor.Bytes, nil
	}

	if lastType.Equals(filtervisitor.Enum) || lastType.Equals(filtervisitor.StrConst) {
		cleanVal := val
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			cleanVal = val[1 : len(val)-1]
		}
		qfv.AppendBindingEnum(cleanVal)
		return lastType, nil
	}

	if lastType.Equals(filtervisitor.Date) {
		cleanVal := val
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			cleanVal = val[1 : len(val)-1]
		}
		_, err := time.Parse("2006-01-02", cleanVal)
		if err != nil {
			goto fallbackString
		}
		qfv.AppendBindingString(cleanVal)
		return filtervisitor.Date, nil
	}

fallbackString:
	// Default/String binding
	cleanVal := val
	if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
		cleanVal = val[1 : len(val)-1]
	}
	qfv.AppendBindingString(cleanVal)
	return filtervisitor.String, nil
}

func PhraseExp(qfv QueryFilterVisitor, p *fpb.Phrase) (filtervisitor.PrimType, error) {
	joined := strings.Join(p.GetValues(), " ")
	quoted := "\"" + joined + "\""
	return ValueExp(qfv, quoted)
}

// Internal optimization/check helpers
func canOptimizeToInList(exprs []*fpb.Expression) bool {
	if len(exprs) < 2 {
		return false
	}
	// Check if all elements are EQUALS restrictions on the same field
	var firstField []string
	for _, expr := range exprs {
		if expr.WhichExpression() != fpb.Expression_Restriction_case {
			return false
		}
		r := expr.GetRestriction()
		if r.GetComparator() != fpb.Restriction_EQUALS {
			return false
		}
		// Must be member on the LHS
		if r.GetLhs().WhichExpression() != fpb.Expression_Member_case {
			return false
		}
		var path []string
		for _, component := range r.GetLhs().GetMember().GetExpressions() {
			if component.WhichExpression() == fpb.Expression_Value_case {
				path = append(path, component.GetValue())
			}
		}
		if len(firstField) == 0 {
			firstField = path
		} else {
			if len(firstField) != len(path) {
				return false
			}
			for idx, part := range path {
				if firstField[idx] != part {
					return false
				}
			}
		}
	}
	return true
}

func buildInListSQL(qfv QueryFilterVisitor, exprs []*fpb.Expression) (safesql.TrustedSQLString, error) {
	// Reconstruct the IN list
	// Format: LHS IN (val1, val2, ...)
	// We want to format this as a standard Spanner query check
	return safesql.EmptyString(), errors.New("in optimization let fallback logic execute")
}

func isLhsTextSearch(lhs *fpb.Expression) bool {
	// A basic keyword HAS search usually has empty/wildcard LHS
	if lhs == nil {
		return true
	}
	if lhs.WhichExpression() == fpb.Expression_Value_case && lhs.GetValue() == "" {
		return true
	}
	return false
}

func handleTextSearch(qfv QueryFilterVisitor, rhs *fpb.Expression) (filtervisitor.PrimType, error) {
	columns, err := qfv.GetTextSearchColumns()
	if err != nil {
		return filtervisitor.Invalid, err
	}
	valStr := ""
	if rhs.WhichExpression() == fpb.Expression_Value_case {
		valStr = rhs.GetValue()
		if (strings.HasPrefix(valStr, "\"") && strings.HasSuffix(valStr, "\"")) || (strings.HasPrefix(valStr, "'") && strings.HasSuffix(valStr, "'")) {
			valStr = valStr[1 : len(valStr)-1]
		}
	} else if rhs.WhichExpression() == fpb.Expression_Phrase_case {
		valStr = strings.Join(rhs.GetPhrase().GetValues(), " ")
	}

	qfv.AppendTrustedSQL(safesql.New("("))
	for idx, col := range columns {
		if idx > 0 {
			qfv.AppendTrustedSQL(safesql.New(" OR "))
		}
		sql, err := qfv.HandleTextSearch(col, valStr)
		if err != nil {
			return filtervisitor.Invalid, err
		}
		qfv.AppendTrustedSQL(sql)
	}
	qfv.AppendTrustedSQL(safesql.New(")"))
	return filtervisitor.Invalid, nil
}

func RegexpLiteral(r *fpb.Restriction) (string, error) {
	if r == nil {
		return "", errors.New("nil restriction")
	}
	if r.GetComparator() != fpb.Restriction_REGEXP_MATCH && r.GetComparator() != fpb.Restriction_REGEXP_NOT_MATCH {
		return "", fmt.Errorf("restriction comparator is not regex match: %v", r.GetComparator())
	}
	rhs := r.GetRhs()
	if rhs == nil {
		return "", errors.New("nil RHS in restriction")
	}
	var val string
	if rhs.WhichExpression() == fpb.Expression_Phrase_case {
		val = strings.Join(rhs.GetPhrase().GetValues(), " ")
	} else if rhs.WhichExpression() == fpb.Expression_Value_case {
		val = rhs.GetValue()
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = val[1 : len(val)-1]
		}
	} else {
		return "", fmt.Errorf("RHS must be a string literal, got %v", rhs.WhichExpression())
	}
	return val, nil
}
