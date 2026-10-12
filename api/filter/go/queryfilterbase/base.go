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

	fpb "github.com/google/xmc/api/filter"
	"github.com/google/xmc/api/filter/go/filtervisitor"
	"github.com/google/xmc/internal/safesql"
)

var IntConst = filtervisitor.CustomType("INT_CONST")

type ExpOptions struct {
	UseFullTextSearch        bool
	UseSubstringTextSearch   bool
	UseHasOperatorAsContains bool
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
	CaptureSQL(fn func() error) (safesql.TrustedSQLString, error)
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

func extractPathAndValue(expr *fpb.Expression) ([]string, string) {
	if expr.WhichExpression() != fpb.Expression_Restriction_case {
		return nil, ""
	}
	r := expr.GetRestriction()
	if r.GetComparator() != fpb.Restriction_EQUALS {
		return nil, ""
	}
	lhs := r.GetLhs()
	rhs := r.GetRhs()

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

	if len(path) == 0 || rhs == nil {
		return path, ""
	}

	var literalVal string
	if rhs.WhichExpression() == fpb.Expression_Phrase_case {
		literalVal = strings.Join(rhs.GetPhrase().GetValues(), " ")
	} else if rhs.WhichExpression() == fpb.Expression_Value_case {
		val := rhs.GetValue()
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = val[1 : len(val)-1]
		}
		literalVal = val
	}
	return path, literalVal
}

func DisjunctionExp(qfv QueryFilterVisitor, el *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	exprs := el.GetExpressions()
	if len(exprs) == 0 {
		return filtervisitor.Invalid, errors.New("empty disjunction list")
	}
	return VisitDisjunction(qfv, exprs)
}

type groupEntry struct {
	path   []string
	exprs  []*fpb.Expression
	values []string
}

func isExprOptimized(g *groupEntry, expr *fpb.Expression) bool {
	if g == nil || len(g.exprs) < 2 {
		return false
	}
	for _, optExpr := range g.exprs {
		if optExpr == expr {
			return true
		}
	}
	return false
}

func VisitDisjunction(qfv QueryFilterVisitor, exprs []*fpb.Expression) (filtervisitor.PrimType, error) {
	groups := make(map[string]*groupEntry)

	for _, expr := range exprs {
		path, literalVal := extractPathAndValue(expr)
		if len(path) > 0 && literalVal != "" && qfv.GetOptimizeDisjunction() {
			if optimizable, _, err := qfv.HandleDisjunction(path); err == nil && optimizable {
				key := strings.Join(path, ".")
				if _, ok := groups[key]; !ok {
					groups[key] = &groupEntry{path: path}
				}
				groups[key].exprs = append(groups[key].exprs, expr)
				groups[key].values = append(groups[key].values, literalVal)
			}
		}
	}

	// Print the final combined OR list in the exact original order!
	qfv.AppendTrustedSQL(safesql.New("("))
	hasFirst := false
	processedGroups := make(map[string]bool)

	for _, expr := range exprs {
		path, _ := extractPathAndValue(expr)
		isOptimized := false
		var key string
		if len(path) > 0 {
			key = strings.Join(path, ".")
			isOptimized = isExprOptimized(groups[key], expr)
		}

		if !isOptimized {
			if hasFirst {
				qfv.AppendTrustedSQL(safesql.New(" OR "))
			}
			if _, err := filtervisitor.Visit(qfv, expr); err != nil {
				return filtervisitor.Invalid, err
			}
			hasFirst = true
			continue
		}

		if !processedGroups[key] {
			processedGroups[key] = true
			g := groups[key]
			_, colSQL, err := qfv.HandleDisjunction(g.path)
			if err != nil {
				return filtervisitor.Invalid, err
			}
			paramSQL, err := qfv.CaptureSQL(func() error {
				qfv.AppendBindingStringArray(g.values)
				return nil
			})
			if err != nil {
				return filtervisitor.Invalid, err
			}

			if hasFirst {
				qfv.AppendTrustedSQL(safesql.New(" OR "))
			}
			qfv.AppendTrustedSQL(safesql.Concat(safesql.New("("), colSQL, safesql.New(" IN UNNEST ("), paramSQL, safesql.New("))")))
			hasFirst = true
		}
	}
	qfv.AppendTrustedSQL(safesql.New(")"))
	return filtervisitor.Invalid, nil
}

func NegationExp(qfv QueryFilterVisitor, e *fpb.Expression) (filtervisitor.PrimType, error) {
	var t filtervisitor.PrimType
	childSQL, err := qfv.CaptureSQL(func() error {
		var visitErr error
		t, visitErr = filtervisitor.Visit(qfv, e)
		return visitErr
	})
	if err != nil {
		return filtervisitor.Invalid, err
	}

	qfv.AppendTrustedSQL(safesql.Concat(safesql.New("NOT "), childSQL))
	return t, nil
}

func handleGlobalRestriction(qfv QueryFilterVisitor, rhs *fpb.Expression) (filtervisitor.PrimType, error) {
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

func checkArrayHasCompatibility(lhsElem, rhsType filtervisitor.PrimType) bool {
	if lhsElem.Compatible(rhsType) {
		return true
	}
	isNumericOrBool := func(n string) bool { return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" || n == "BOOLEAN" }
	isStringy := func(n string) bool { return n == "STRING" || n == "ENUM" || n == "STR_CONST" }
	return isNumericOrBool(lhsElem.Name) && isStringy(rhsType.Name)
}

func handleHasRestriction(qfv QueryFilterVisitor, lhs, rhs *fpb.Expression, lhsType, rhsType filtervisitor.PrimType, lhsSQL, rhsSQL safesql.TrustedSQLString) (filtervisitor.PrimType, error) {
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
		if !checkArrayHasCompatibility(lhsArrayElemType, rhsType) {
			return filtervisitor.Invalid, fmt.Errorf("type mismatch in array HAS: array element is %v, RHS is %v", lhsArrayElemType.Name, rhsType.Name)
		}
		qfv.AppendTrustedSQL(safesql.Concat(safesql.New("("), rhsSQL, safesql.New(" IN "), lhsSQL, safesql.New(")")))
		return filtervisitor.Invalid, nil
	}

	if !lhsType.Equals(filtervisitor.String) && !lhsType.Equals(filtervisitor.Bytes) {
		return filtervisitor.Invalid, fmt.Errorf("HAS operator is not supported for type %v", lhsType.Name)
	}
	if !qfv.GetExpOptions().UseHasOperatorAsContains {
		return filtervisitor.Invalid, fmt.Errorf("HAS operator is not supported for string/bytes fields unless has contains option is enabled")
	}
	if !lhsType.Compatible(rhsType) {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch in HAS: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
	}

	if qfv.GetExpOptions().UseHasOperatorAsContains {
		qfv.AppendTrustedSQL(safesql.Concat(safesql.New("(STRPOS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New(") > 0)")))
		return filtervisitor.Invalid, nil
	}

	qfv.AppendTrustedSQL(safesql.Concat(safesql.New("(REGEXP_CONTAINS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New("))")))
	return filtervisitor.Invalid, nil
}

func handleRegexpRestriction(qfv QueryFilterVisitor, op fpb.Restriction_Comparator, lhsType, rhsType filtervisitor.PrimType, lhsSQL, rhsSQL safesql.TrustedSQLString) (filtervisitor.PrimType, error) {
	if !lhsType.Equals(filtervisitor.String) && !lhsType.Equals(filtervisitor.Bytes) {
		return filtervisitor.Invalid, fmt.Errorf("regexp matching operator is not supported for type %v", lhsType.Name)
	}
	if !lhsType.Compatible(rhsType) {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch in regexp match: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
	}

	if op == fpb.Restriction_REGEXP_MATCH {
		qfv.AppendTrustedSQL(safesql.Concat(safesql.New("(REGEXP_CONTAINS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New("))")))
	} else {
		qfv.AppendTrustedSQL(safesql.Concat(safesql.New("(NOT (REGEXP_CONTAINS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New(")))")))
	}
	return filtervisitor.Invalid, nil
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
		return handleGlobalRestriction(qfv, rhs)
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
	var lhsType filtervisitor.PrimType
	lhsSQL, err := qfv.CaptureSQL(func() error {
		var visitErr error
		lhsType, visitErr = filtervisitor.Visit(qfv, lhs)
		return visitErr
	})
	if err != nil {
		return filtervisitor.Invalid, err
	}

	// Propagate lastType and capture RHS!
	qfv.SetLastType(lhsType)
	qfv.SetIsRHS(true)
	var rhsType filtervisitor.PrimType
	rhsSQL, err := qfv.CaptureSQL(func() error {
		var visitErr error
		rhsType, visitErr = filtervisitor.Visit(qfv, rhs)
		return visitErr
	})
	qfv.SetIsRHS(false)
	if err != nil {
		return filtervisitor.Invalid, err
	}

	// Enforce strict/legacy-fallback type compatibility!
	isCompatible := lhsType.Compatible(rhsType)

	if op == fpb.Restriction_HAS {
		return handleHasRestriction(qfv, lhs, rhs, lhsType, rhsType, lhsSQL, rhsSQL)
	}

	if op == fpb.Restriction_REGEXP_MATCH || op == fpb.Restriction_REGEXP_NOT_MATCH {
		return handleRegexpRestriction(qfv, op, lhsType, rhsType, lhsSQL, rhsSQL)
	}

	// Standard comparison type safety verification!
	if !isCompatible && (op == fpb.Restriction_EQUALS || op == fpb.Restriction_NOT_EQUALS) {
		isNumericOrBool := func(n string) bool {
			return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" || n == "BOOLEAN" || n == "TIMESTAMP" || n == "DATE" || n == "PROTO_TIMESTAMP"
		}
		isStringy := func(n string) bool { return n == "STRING" || n == "ENUM" || n == "STR_CONST" }
		if isNumericOrBool(lhsType.Name) && isStringy(rhsType.Name) {
			isCompatible = true
		}
	}
	if !isCompatible {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
	}

	// Format comparison operators
	var opSQL safesql.TrustedSQLString
	switch op {
	case fpb.Restriction_EQUALS:
		opSQL = safesql.New(" = ")
	case fpb.Restriction_NOT_EQUALS:
		opSQL = safesql.New(" != ")
	case fpb.Restriction_LESS_THAN:
		opSQL = safesql.New(" < ")
	case fpb.Restriction_LESS_EQUALS:
		opSQL = safesql.New(" <= ")
	case fpb.Restriction_GREATER_THAN:
		opSQL = safesql.New(" > ")
	case fpb.Restriction_GREATER_EQUALS:
		opSQL = safesql.New(" >= ")
	default:
		opSQL = safesql.New(" = ")
	}

	qfv.AppendTrustedSQL(safesql.Concat(safesql.New("("), lhsSQL, opSQL, rhsSQL, safesql.New(")")))
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

func parseTimestampVal(val string) (time.Time, bool) {
	cleanVal := val
	if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
		cleanVal = val[1 : len(val)-1]
	}
	if t, err := time.Parse(time.RFC3339, cleanVal); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", cleanVal); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func bindIntegerOrUnsigned(qfv QueryFilterVisitor, lastType filtervisitor.PrimType, val string) (filtervisitor.PrimType, bool, error) {
	if lastType.Equals(filtervisitor.Unsigned) {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil && i < 0 {
			return filtervisitor.Invalid, false, fmt.Errorf("negative value %v is out of bounds for unsigned field", val)
		}
	}

	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		qfv.AppendBindingInt64(i)
		return filtervisitor.Integer, true, nil
	}
	if u, errOpt := strconv.ParseUint(val, 10, 64); errOpt == nil {
		qfv.AppendBindingUInt64(u)
		return filtervisitor.Unsigned, true, nil
	}
	return filtervisitor.Invalid, false, nil
}

func ValueExp(qfv QueryFilterVisitor, val string) (filtervisitor.PrimType, error) {
	return VisitValue(qfv, val)
}

func VisitValue(qfv QueryFilterVisitor, val string) (filtervisitor.PrimType, error) {
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
		t, ok, err := bindIntegerOrUnsigned(qfv, lastType, val)
		if err != nil {
			return filtervisitor.Invalid, err
		}
		if ok {
			return t, nil
		}
		goto fallbackString
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
		if t, ok := parseTimestampVal(val); ok {
			qfv.AppendBindingTimestamp(t)
			return lastType, nil
		}
		goto fallbackString
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
