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

	fpb "xmanager_cloud/api/filter"
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/safesql"
)

var IntConst = filtervisitor.CustomType("INT_CONST")

type ExpOptions struct {
	UseFullTextSearch        bool
	UseSubstringTextSearch   bool
	UseHasOperatorAsContains bool
}

// Dialect renders the SQL predicates whose syntax differs between database backends.
type Dialect interface {
	// InArray matches rows where col equals any element of the bound array parameter arr.
	InArray(col, arr safesql.TrustedSQLString) safesql.TrustedSQLString
	// ArrayContains matches rows where the array column arr contains elem.
	ArrayContains(arr, elem safesql.TrustedSQLString) safesql.TrustedSQLString
	// RegexpMatch matches rows where lhs matches the regular expression rhs.
	RegexpMatch(lhs, rhs safesql.TrustedSQLString) safesql.TrustedSQLString
	// TextSearch matches rows where the full-text search column col matches queryParam.
	TextSearch(col, queryParam safesql.TrustedSQLString) safesql.TrustedSQLString
	// OrderByClause renders a single ORDER BY column expression with sort direction and
	// dialect-appropriate NULL ordering (NULLS FIRST for ASC, NULLS LAST for DESC).
	OrderByClause(col safesql.TrustedSQLString, desc, nullable bool) safesql.TrustedSQLString
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
	Dialect() Dialect
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
		literalVal, _ = stripQuotes(rhs.GetValue())
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

func collectDisjunctionGroups(qfv QueryFilterVisitor, exprs []*fpb.Expression) map[string]*groupEntry {
	groups := make(map[string]*groupEntry)
	if !qfv.GetOptimizeDisjunction() {
		return groups
	}
	for _, expr := range exprs {
		path, literalVal := extractPathAndValue(expr)
		if len(path) == 0 || literalVal == "" {
			continue
		}
		if optimizable, _, err := qfv.HandleDisjunction(path); err != nil || !optimizable {
			continue
		}
		key := strings.Join(path, ".")
		if _, ok := groups[key]; !ok {
			groups[key] = &groupEntry{path: path}
		}
		groups[key].exprs = append(groups[key].exprs, expr)
		groups[key].values = append(groups[key].values, literalVal)
	}
	return groups
}

func appendOptimizedDisjunctionGroup(qfv QueryFilterVisitor, g *groupEntry) error {
	_, colSQL, err := qfv.HandleDisjunction(g.path)
	if err != nil {
		return err
	}
	paramSQL, err := qfv.CaptureSQL(func() error {
		qfv.AppendBindingStringArray(g.values)
		return nil
	})
	if err != nil {
		return err
	}
	qfv.AppendTrustedSQL(qfv.Dialect().InArray(colSQL, paramSQL))
	return nil
}

func VisitDisjunction(qfv QueryFilterVisitor, exprs []*fpb.Expression) (filtervisitor.PrimType, error) {
	groups := collectDisjunctionGroups(qfv, exprs)

	// Print the final combined OR list in the exact original order!
	qfv.AppendTrustedSQL(safesql.New("("))
	hasFirst := false
	processedGroups := make(map[string]bool)

	for _, expr := range exprs {
		path, _ := extractPathAndValue(expr)
		key := strings.Join(path, ".")
		if len(path) > 0 && isExprOptimized(groups[key], expr) {
			if processedGroups[key] {
				continue
			}
			processedGroups[key] = true
			if hasFirst {
				qfv.AppendTrustedSQL(safesql.New(" OR "))
			}
			if err := appendOptimizedDisjunctionGroup(qfv, groups[key]); err != nil {
				return filtervisitor.Invalid, err
			}
			hasFirst = true
			continue
		}

		if hasFirst {
			qfv.AppendTrustedSQL(safesql.New(" OR "))
		}
		if _, err := filtervisitor.Visit(qfv, expr); err != nil {
			return filtervisitor.Invalid, err
		}
		hasFirst = true
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

func isNumericOrBoolType(name string) bool {
	return name == "INTEGER" || name == "UNSIGNED" || name == "FLOAT" || name == "BOOLEAN"
}

func isNumericBoolOrTimeType(name string) bool {
	return isNumericOrBoolType(name) || name == "TIMESTAMP" || name == "DATE" || name == "PROTO_TIMESTAMP"
}

func isStringyType(name string) bool {
	return name == "STRING" || name == "ENUM" || name == "STR_CONST"
}

func stripQuotes(val string) (string, bool) {
	if len(val) >= 2 && ((strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'"))) {
		return val[1 : len(val)-1], true
	}
	return val, false
}

func checkArrayHasCompatibility(lhsElem, rhsType filtervisitor.PrimType) bool {
	if lhsElem.Compatible(rhsType) {
		return true
	}
	return isNumericOrBoolType(lhsElem.Name) && isStringyType(rhsType.Name)
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
		qfv.AppendTrustedSQL(qfv.Dialect().ArrayContains(lhsSQL, rhsSQL))
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

	qfv.AppendTrustedSQL(safesql.Concat(safesql.New("(STRPOS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New(") > 0)")))
	return filtervisitor.Invalid, nil
}

func handleRegexpRestriction(qfv QueryFilterVisitor, op fpb.Restriction_Comparator, lhsType, rhsType filtervisitor.PrimType, lhsSQL, rhsSQL safesql.TrustedSQLString) (filtervisitor.PrimType, error) {
	if !lhsType.Equals(filtervisitor.String) && !lhsType.Equals(filtervisitor.Bytes) {
		return filtervisitor.Invalid, fmt.Errorf("regexp matching operator is not supported for type %v", lhsType.Name)
	}
	if !lhsType.Compatible(rhsType) {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch in regexp match: LHS is %v, RHS is %v", lhsType.Name, rhsType.Name)
	}

	match := qfv.Dialect().RegexpMatch(lhsSQL, rhsSQL)
	if op != fpb.Restriction_REGEXP_MATCH {
		match = safesql.Concat(safesql.New("(NOT "), match, safesql.New(")"))
	}
	qfv.AppendTrustedSQL(match)
	return filtervisitor.Invalid, nil
}

type restrictionSide struct {
	typ filtervisitor.PrimType
	sql safesql.TrustedSQLString
}

func validateRestrictionRHS(rhs *fpb.Expression) error {
	if rhs == nil {
		return nil
	}
	w := rhs.WhichExpression()
	if w == fpb.Expression_Conjunction_case || w == fpb.Expression_Disjunction_case || w == fpb.Expression_Negation_case {
		return fmt.Errorf("RHS cannot contain logical operators (AND/OR/NOT)")
	}
	return nil
}

func captureRestrictionSide(qfv QueryFilterVisitor, expr *fpb.Expression) (restrictionSide, error) {
	var side restrictionSide
	capturedSQL, err := qfv.CaptureSQL(func() error {
		var visitErr error
		side.typ, visitErr = filtervisitor.Visit(qfv, expr)
		return visitErr
	})
	side.sql = capturedSQL
	return side, err
}

func areComparisonTypesCompatible(op fpb.Restriction_Comparator, lhsType, rhsType filtervisitor.PrimType) bool {
	if lhsType.Compatible(rhsType) {
		return true
	}
	if op != fpb.Restriction_EQUALS && op != fpb.Restriction_NOT_EQUALS {
		return false
	}
	return isNumericBoolOrTimeType(lhsType.Name) && isStringyType(rhsType.Name)
}

func comparisonOperatorSQL(op fpb.Restriction_Comparator) safesql.TrustedSQLString {
	switch op {
	case fpb.Restriction_EQUALS:
		return safesql.New(" = ")
	case fpb.Restriction_NOT_EQUALS:
		return safesql.New(" != ")
	case fpb.Restriction_LESS_THAN:
		return safesql.New(" < ")
	case fpb.Restriction_LESS_EQUALS:
		return safesql.New(" <= ")
	case fpb.Restriction_GREATER_THAN:
		return safesql.New(" > ")
	case fpb.Restriction_GREATER_EQUALS:
		return safesql.New(" >= ")
	default:
		return safesql.New(" = ")
	}
}

func RestrictionExp(qfv QueryFilterVisitor, r *fpb.Restriction) (filtervisitor.PrimType, error) {
	if ok, rewritten := qfv.TranslateRestriction(r); ok && rewritten != r {
		return filtervisitor.Visit(qfv, &fpb.Expression{
			Expression: &fpb.Expression_Restriction{
				Restriction: rewritten,
			},
		})
	}

	lhs, rhs, op := r.GetLhs(), r.GetRhs(), r.GetComparator()
	if err := validateRestrictionRHS(rhs); err != nil {
		return filtervisitor.Invalid, err
	}
	if op == fpb.Restriction_GLOBAL {
		return handleGlobalRestriction(qfv, rhs)
	}
	if op == fpb.Restriction_HAS && isLhsTextSearch(lhs) {
		return handleTextSearch(qfv, rhs)
	}
	if op == fpb.Restriction_HAS {
		qfv.SetIsHAS(true)
		defer qfv.SetIsHAS(false)
	}

	lhsSide, err := captureRestrictionSide(qfv, lhs)
	if err != nil {
		return filtervisitor.Invalid, err
	}
	qfv.SetLastType(lhsSide.typ)
	qfv.SetIsRHS(true)
	rhsSide, err := captureRestrictionSide(qfv, rhs)
	qfv.SetIsRHS(false)
	if err != nil {
		return filtervisitor.Invalid, err
	}

	if op == fpb.Restriction_HAS {
		return handleHasRestriction(qfv, lhs, rhs, lhsSide.typ, rhsSide.typ, lhsSide.sql, rhsSide.sql)
	}
	if op == fpb.Restriction_REGEXP_MATCH || op == fpb.Restriction_REGEXP_NOT_MATCH {
		return handleRegexpRestriction(qfv, op, lhsSide.typ, rhsSide.typ, lhsSide.sql, rhsSide.sql)
	}
	if !areComparisonTypesCompatible(op, lhsSide.typ, rhsSide.typ) {
		return filtervisitor.Invalid, fmt.Errorf("type mismatch: LHS is %v, RHS is %v", lhsSide.typ.Name, rhsSide.typ.Name)
	}

	qfv.AppendTrustedSQL(safesql.Concat(safesql.New("("), lhsSide.sql, comparisonOperatorSQL(op), rhsSide.sql, safesql.New(")")))
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

func parseTimestampVal(cleanVal string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, cleanVal); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.DateOnly, cleanVal); err == nil {
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

func resolveUnquotedRHSMember(qfv QueryFilterVisitor, val string, lastType filtervisitor.PrimType) (filtervisitor.PrimType, bool) {
	if lastType.Equals(filtervisitor.Enum) || lastType.Equals(filtervisitor.StrConst) {
		return filtervisitor.Invalid, false
	}
	sql, t, err := qfv.HandleMember([]string{val})
	if err != nil {
		return filtervisitor.Invalid, false
	}
	qfv.AppendTrustedSQL(sql)
	return t, true
}

func checkStringyLiteralTypeMismatch(val string, isHAS bool, lastType filtervisitor.PrimType) (filtervisitor.PrimType, bool) {
	if isHAS || (!isStringyType(lastType.Name) && !lastType.Equals(filtervisitor.Bytes)) {
		return filtervisitor.Invalid, false
	}
	if _, err := strconv.ParseFloat(val, 64); err == nil {
		return filtervisitor.Float, true // Returns numeric to trigger type mismatch!
	}
	if val == "true" || val == "false" {
		return filtervisitor.Boolean, true // Returns boolean to trigger type mismatch!
	}
	return filtervisitor.Invalid, false
}

func bindTemporalValue(qfv QueryFilterVisitor, lastType filtervisitor.PrimType, cleanVal string) (filtervisitor.PrimType, bool) {
	if lastType.Equals(filtervisitor.Timestamp) || lastType.Equals(filtervisitor.ProtoTimestamp) {
		t, ok := parseTimestampVal(cleanVal)
		if !ok {
			return filtervisitor.Invalid, false
		}
		qfv.AppendBindingTimestamp(t)
		return lastType, true
	}
	if lastType.Equals(filtervisitor.Date) {
		if _, err := time.Parse(time.DateOnly, cleanVal); err != nil {
			return filtervisitor.Invalid, false
		}
		qfv.AppendBindingString(cleanVal)
		return filtervisitor.Date, true
	}
	return filtervisitor.Invalid, false
}

func bindTypedValue(qfv QueryFilterVisitor, lastType filtervisitor.PrimType, val, cleanVal string, hasQuotes bool) (filtervisitor.PrimType, bool, error) {
	switch {
	case (lastType.Equals(filtervisitor.Integer) || lastType.Equals(filtervisitor.Unsigned)) && !hasQuotes:
		return bindIntegerOrUnsigned(qfv, lastType, val)
	case lastType.Equals(filtervisitor.Float):
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			//nolint:nilerr // handled=false falls through to default string binding
			return filtervisitor.Invalid, false, nil
		}
		qfv.AppendBindingFloat64(f)
		return filtervisitor.Float, true, nil
	case lastType.Equals(filtervisitor.Boolean):
		b, err := strconv.ParseBool(val)
		if err != nil {
			//nolint:nilerr // handled=false falls through to default string binding
			return filtervisitor.Invalid, false, nil
		}
		qfv.AppendBindingBool(b)
		return filtervisitor.Boolean, true, nil
	case lastType.Equals(filtervisitor.Timestamp) || lastType.Equals(filtervisitor.ProtoTimestamp) || lastType.Equals(filtervisitor.Date):
		t, ok := bindTemporalValue(qfv, lastType, cleanVal)
		return t, ok, nil
	case lastType.Equals(filtervisitor.Bytes):
		qfv.AppendBindingByteArray([]byte(cleanVal))
		return filtervisitor.Bytes, true, nil
	case lastType.Equals(filtervisitor.Enum) || lastType.Equals(filtervisitor.StrConst):
		qfv.AppendBindingEnum(cleanVal)
		return lastType, true, nil
	default:
		return filtervisitor.Invalid, false, nil
	}
}

func resolveUnquotedValue(qfv QueryFilterVisitor, val string, lastType filtervisitor.PrimType) (filtervisitor.PrimType, bool) {
	if qfv.GetIsRHS() {
		if t, ok := resolveUnquotedRHSMember(qfv, val, lastType); ok {
			return t, true
		}
	}
	// Rule B: Flag unquoted numeric/boolean literals on stringy fields as mismatched types!
	return checkStringyLiteralTypeMismatch(val, qfv.GetIsHAS(), lastType)
}

func VisitValue(qfv QueryFilterVisitor, val string) (filtervisitor.PrimType, error) {
	cleanVal, hasQuotes := stripQuotes(val)

	if !qfv.GetIsRHS() && !hasQuotes {
		sql, t, err := qfv.HandleMember([]string{val})
		if err != nil {
			return filtervisitor.Invalid, err
		}
		qfv.AppendTrustedSQL(sql)
		return t, nil
	}

	lastType := qfv.GetLastType()
	if !hasQuotes {
		if t, ok := resolveUnquotedValue(qfv, val, lastType); ok {
			return t, nil
		}
	}

	// Rule A: Quoted literals on numeric/boolean LHS go straight to default string binding!
	if !(hasQuotes && isNumericOrBoolType(lastType.Name)) {
		if t, handled, err := bindTypedValue(qfv, lastType, val, cleanVal, hasQuotes); err != nil || handled {
			return t, err
		}
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
		valStr, _ = stripQuotes(rhs.GetValue())
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
		val, _ = stripQuotes(rhs.GetValue())
	} else {
		return "", fmt.Errorf("RHS must be a string literal, got %v", rhs.WhichExpression())
	}
	return val, nil
}
