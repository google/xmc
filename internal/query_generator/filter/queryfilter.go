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
// Package queryfilter defines a method for augmenting Spanner queries with additional WHERE
// predicates derived from an AIP-160 filtering expression.
package queryfilter

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"api/filter/go/filtervisitor"
	"api/filter/go/queryfilterbase"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"googlesql/safesql/go/safesql"

	fpb "api/filter/filter_expression_go_proto"
)

// MemberFunc represents a callback that maps a dynamic query member field path to a database representation.
type MemberFunc func([]string) (safesql.TrustedSQLString, filtervisitor.PrimType, error)

// FunctionFunc is the callback used to process function calls when augmenting a query with a filter.
// The filtervisitor.PrimType returned is the primitive type returned by the function (filtervisitor.Invalid on error).
type FunctionFunc func(functionName string, s *Spanner, args []*fpb.Expression) (filtervisitor.PrimType, error)

// DisjunctionFunc returns the member itself allowing collection of members and values in the case of a grouping.
type DisjunctionFunc func([]string) (bool, safesql.TrustedSQLString, error)

// RestrictionTranslate takes a restriction AST (represented by the expression graph) and transforms it into another restriction.
// Note that it must not change the type of the restriction, it is only designed to modify the left and right hand sides.
type RestrictionTranslate func(r *fpb.Restriction) *fpb.Restriction

// FilterBaseOption will set options for the queryfilterbase.
type FilterBaseOption func(qf *Spanner)

// WithFullTextSearchEnabled enables full text search for the queryfilterbase.
func WithFullTextSearchEnabled(qf *Spanner) {
	qf.filterBaseExpOptions.UseFullTextSearch = true
}

// WithSubstringTextSearchEnabled enables substring text search for the queryfilterbase.
func WithSubstringTextSearchEnabled(qf *Spanner) {
	qf.filterBaseExpOptions.UseSubstringTextSearch = true
}

// WithHasOperatorAsContains enables the HAS operator to be used as a contains operator for string fields.
func WithHasOperatorAsContains(qf *Spanner) {
	qf.filterBaseExpOptions.UseHasOperatorAsContains = true
}

// Spanner appends a filter expression to a spanner.Query using the provided MemberFunc for appending member accesses.
// Implements the filtervisitor interface. This struct is not threadsafe, as it modifies the spanner query.
type Spanner struct {
	MemberFunc   MemberFunc
	FunctionFunc FunctionFunc

	// Whether to convert a query of the type ` a = 1 or a = 2 or a = 3` to a in [1, 2, 3] to optimize query compilation.
	OptimizeDisjunction bool
	// Provides direct access to a member before appending to the query.
	DisjunctionFunc DisjunctionFunc

	// count is used by appendBinding to uniquely name query bindings.
	count uint64
	Q     *spanner.Statement

	lastType filtervisitor.PrimType

	// An optional function which rewrites restrictions.
	RestrictionTranslate RestrictionTranslate

	// TextSearchColumns is the list of columns to use for text search in spanner.
	// See:
	// (internal URL)
	TextSearchColumns []safesql.TrustedSQLString

	// FilterBaseOptions are options for the queryfilterbase.
	FilterBaseOptions    []FilterBaseOption
	filterBaseExpOptions *queryfilterbase.ExpOptions
	isRHS                bool
	isHAS                bool
}

// AppendFilters augments the given query with a predicate expression derived from the parsed filter expression.
// The supplied query should be ready for a parenthesized predicate to be directly added, for examples:
//
//	`SELECT p.foo FROM Projects p WHERE `, and
//	`SELECT p.foo FROM Projects p WHERE p.bar != 3 AND `
func (qf *Spanner) AppendFilters(e *fpb.Expression) error {
	return queryfilterbase.AppendFilters(qf, e)
}

// ConjunctionExp implements filtervisitor.FilterVisitor
func (qf *Spanner) ConjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.ConjunctionExp(qf, e)
}

// DisjunctionExp implements filtervisitor.FilterVisitor
func (qf *Spanner) DisjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.DisjunctionExp(qf, e)
}

// NegationExp implements filtervisitor.FilterVisitor
func (qf *Spanner) NegationExp(e *fpb.Expression) (filtervisitor.PrimType, error) {
	return queryfilterbase.NegationExp(qf, e)
}

// RestrictionExp implements filtervisitor.FilterVisitor
func (qf *Spanner) RestrictionExp(e *fpb.Restriction) (filtervisitor.PrimType, error) {
	return queryfilterbase.RestrictionExp(qf, e)
}

// MemberExp implements filtervisitor.FilterVisitor
func (qf *Spanner) MemberExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.MemberExp(qf, e)
}

// FunctionExp implements filtervisitor.FilterVisitor
func (qf *Spanner) FunctionExp(e *fpb.Function) (filtervisitor.PrimType, error) {
	return queryfilterbase.FunctionExp(qf, e)
}

// ValueExp implements filtervisitor.FilterVisitor
func (qf *Spanner) ValueExp(e string) (filtervisitor.PrimType, error) {
	return queryfilterbase.ValueExp(qf, e)
}

// PhraseExp implements filtervisitor.FilterVisitor
func (qf *Spanner) PhraseExp(e *fpb.Phrase) (filtervisitor.PrimType, error) {
	return queryfilterbase.PhraseExp(qf, e)
}

// GetOptimizeDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetOptimizeDisjunction() bool {
	return qf.OptimizeDisjunction
}

// SetLastType implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) SetLastType(t filtervisitor.PrimType) {
	qf.lastType = t
}

// GetLastType implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetLastType() filtervisitor.PrimType {
	return qf.lastType
}

// HandleMember implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) HandleMember(path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
	if qf.MemberFunc == nil {
		return safesql.New(""), filtervisitor.Invalid, errors.New("handling member is not implemented")
	}
	return qf.MemberFunc(path)
}

// HandleFunction implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) HandleFunction(functionName string, args []*fpb.Expression) (bool, filtervisitor.PrimType, error) {
	if qf.FunctionFunc == nil {
		return false, filtervisitor.Invalid, nil
	}
	t, err := qf.FunctionFunc(functionName, qf, args)
	return true, t, err
}

// TranslateRestriction implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) TranslateRestriction(r *fpb.Restriction) (bool, *fpb.Restriction) {
	if qf.RestrictionTranslate == nil {
		return false, nil
	}
	return true, qf.RestrictionTranslate(r)
}

// HandleDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) HandleDisjunction(value []string) (bool, safesql.TrustedSQLString, error) {
	if qf.DisjunctionFunc == nil {
		return false, safesql.New(""), errors.New("handling disjunction is not implemented")
	}
	return qf.DisjunctionFunc(value)
}

// GetTextSearchColumns implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetTextSearchColumns() ([]safesql.TrustedSQLString, error) {
	if len(qf.TextSearchColumns) == 0 {
		return nil, errors.New("full text search column is not implemented")
	}
	return qf.TextSearchColumns, nil
}

// GetExpOptions implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetExpOptions() *queryfilterbase.ExpOptions {
	if qf.filterBaseExpOptions == nil {
		qf.filterBaseExpOptions = &queryfilterbase.ExpOptions{}
	}

	for _, opt := range qf.FilterBaseOptions {
		opt(qf)
	}

	return qf.filterBaseExpOptions
}

func (qf *Spanner) appendBinding(v any) {
	index := atomic.AddUint64(&qf.count, 1) - 1

	// Append the binding name to the query
	qf.Q.SQL = qf.Q.SQL + fmt.Sprintf("@fb%d", index)

	// Bind the value.
	if qf.Q.Params == nil {
		qf.Q.Params = make(map[string]any)
	}
	paramKey := fmt.Sprintf("fb%d", index)
	qf.Q.Params[paramKey] = v
}

// AppendBindingTimestamp implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingTimestamp(t time.Time) {
	qf.appendBinding(t)
}

// AppendBindingString implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingString(s string) {
	qf.appendBinding(s)
}

// AppendBindingStringArray implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingStringArray(s []string) {
	qf.appendBinding(s)
}

// AppendBindingByteArray implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingByteArray(b []byte) {
	qf.appendBinding(b)
}

// AppendBindingInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingInt64(i int64) {
	qf.appendBinding(i)
}

// AppendBindingUInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingUInt64(i uint64) {
	qf.appendBinding(i)
}

// AppendBindingFloat64 implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingFloat64(f float64) {
	qf.appendBinding(f)
}

// AppendBindingBool implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingBool(b bool) {
	qf.appendBinding(b)
}

// AppendBindingEnum implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendBindingEnum(e string) {
	qf.appendBinding(e)
}

// AppendTrustedSQL implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) AppendTrustedSQL(sql safesql.TrustedSQLString) {
	qf.Q.SQL = qf.Q.SQL + sql.String()
}

func (qf *Spanner) SetIsRHS(isRHS bool) {
	qf.isRHS = isRHS
}

func (qf *Spanner) GetIsRHS() bool {
	return qf.isRHS
}

func (qf *Spanner) SetIsHAS(isHAS bool) {
	qf.isHAS = isHAS
}

func (qf *Spanner) GetIsHAS() bool {
	return qf.isHAS
}

func (qf *Spanner) GetSQL() string {
	if qf.Q == nil {
		return ""
	}
	return qf.Q.SQL
}

func (qf *Spanner) SetSQL(s string) {
	if qf.Q != nil {
		qf.Q.SQL = s
	}
}

func (qf *Spanner) HandleTextSearch(col safesql.TrustedSQLString, val string) (safesql.TrustedSQLString, error) {
	oldSQL := qf.GetSQL()
	qf.SetSQL("")
	qf.AppendBindingString(val)
	paramSQL := qf.GetSQL()
	qf.SetSQL(oldSQL)

	return safesql.New("SEARCH(" + col.String() + ", " + paramSQL + ")"), nil
}
