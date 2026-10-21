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
// Package queryfilter defines a method for augmenting Spanner and PostgreSQL queries with additional WHERE
// predicates derived from an AIP-160 filtering expression.
package queryfilter

import (
	"errors"
	"time"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"

	fpb "xmanager_cloud/api/filter"
)

// MemberFunc represents a callback that maps a dynamic query member field path to a database representation.
type MemberFunc func([]string) (safesql.TrustedSQLString, filtervisitor.PrimType, error)

// FunctionFunc is the callback used to process function calls when augmenting a query with a filter.
// The filtervisitor.PrimType returned is the primitive type returned by the function (filtervisitor.Invalid on error).
type FunctionFunc func(functionName string, v *Visitor, args []*fpb.Expression) (filtervisitor.PrimType, error)

// DisjunctionFunc returns the member itself allowing collection of members and values in the case of a grouping.
type DisjunctionFunc func([]string) (bool, safesql.TrustedSQLString, error)

// RestrictionTranslate takes a restriction AST (represented by the expression graph) and transforms it into another restriction.
// Note that it must not change the type of the restriction, it is only designed to modify the left and right hand sides.
type RestrictionTranslate func(r *fpb.Restriction) *fpb.Restriction

// FilterBaseOption will set options for the queryfilterbase.
type FilterBaseOption func(opts *queryfilterbase.ExpOptions)

// WithFullTextSearchEnabled enables full text search for the queryfilterbase.
func WithFullTextSearchEnabled(opts *queryfilterbase.ExpOptions) {
	opts.UseFullTextSearch = true
}

// WithSubstringTextSearchEnabled enables substring text search for the queryfilterbase.
func WithSubstringTextSearchEnabled(opts *queryfilterbase.ExpOptions) {
	opts.UseSubstringTextSearch = true
}

// WithHasOperatorAsContains enables the HAS operator to be used as a contains operator for string fields.
func WithHasOperatorAsContains(opts *queryfilterbase.ExpOptions) {
	opts.UseHasOperatorAsContains = true
}

var _ queryfilterbase.QueryFilterVisitor = (*Visitor)(nil)

// Visitor appends a filter expression to a Statement using the provided MemberFunc for appending member accesses.
// Implements the filtervisitor interface. This struct is not threadsafe, as it modifies the query.
type Visitor struct {
	MemberFunc   MemberFunc
	FunctionFunc FunctionFunc

	// Whether to convert a query of the type ` a = 1 or a = 2 or a = 3` to a in [1, 2, 3] to optimize query compilation.
	OptimizeDisjunction bool
	// Provides direct access to a member before appending to the query.
	DisjunctionFunc DisjunctionFunc

	// Q is the statement the filter is appended to. Its type selects the SQL dialect.
	Q Statement

	lastType filtervisitor.PrimType

	// An optional function which rewrites restrictions.
	RestrictionTranslate RestrictionTranslate

	// TextSearchColumns is the list of columns to use for text search (Spanner token columns or PostgreSQL TSVECTOR columns).
	// See:
	// (internal URL)
	TextSearchColumns []safesql.TrustedSQLString

	// FilterBaseOptions are options for the queryfilterbase.
	FilterBaseOptions    []FilterBaseOption
	filterBaseExpOptions *queryfilterbase.ExpOptions
	isRHS                bool
	isHAS                bool
	captureStack         []safesql.TrustedSQLString
}

// AppendFilters augments the given query with a predicate expression derived from the parsed filter expression.
// The supplied query should be ready for a parenthesized predicate to be directly added, for examples:
//
//	`SELECT p.foo FROM Projects p WHERE `, and
//	`SELECT p.foo FROM Projects p WHERE p.bar != 3 AND `
func (qf *Visitor) AppendFilters(e *fpb.Expression) error {
	return queryfilterbase.AppendFilters(qf, e)
}

// ConjunctionExp implements filtervisitor.FilterVisitor
func (qf *Visitor) ConjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.ConjunctionExp(qf, e)
}

// DisjunctionExp implements filtervisitor.FilterVisitor
func (qf *Visitor) DisjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.DisjunctionExp(qf, e)
}

// NegationExp implements filtervisitor.FilterVisitor
func (qf *Visitor) NegationExp(e *fpb.Expression) (filtervisitor.PrimType, error) {
	return queryfilterbase.NegationExp(qf, e)
}

// RestrictionExp implements filtervisitor.FilterVisitor
func (qf *Visitor) RestrictionExp(e *fpb.Restriction) (filtervisitor.PrimType, error) {
	return queryfilterbase.RestrictionExp(qf, e)
}

// MemberExp implements filtervisitor.FilterVisitor
func (qf *Visitor) MemberExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.MemberExp(qf, e)
}

// FunctionExp implements filtervisitor.FilterVisitor
func (qf *Visitor) FunctionExp(e *fpb.Function) (filtervisitor.PrimType, error) {
	return queryfilterbase.FunctionExp(qf, e)
}

// ValueExp implements filtervisitor.FilterVisitor
func (qf *Visitor) ValueExp(e string) (filtervisitor.PrimType, error) {
	return queryfilterbase.ValueExp(qf, e)
}

// PhraseExp implements filtervisitor.FilterVisitor
func (qf *Visitor) PhraseExp(e *fpb.Phrase) (filtervisitor.PrimType, error) {
	return queryfilterbase.PhraseExp(qf, e)
}

// GetOptimizeDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) GetOptimizeDisjunction() bool {
	return qf.OptimizeDisjunction
}

// SetLastType implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) SetLastType(t filtervisitor.PrimType) {
	qf.lastType = t
}

// GetLastType implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) GetLastType() filtervisitor.PrimType {
	return qf.lastType
}

// HandleMember implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) HandleMember(path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
	if qf.MemberFunc == nil {
		return safesql.New(""), filtervisitor.Invalid, errors.New("handling member is not implemented")
	}
	return qf.MemberFunc(path)
}

// HandleFunction implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) HandleFunction(functionName string, args []*fpb.Expression) (bool, filtervisitor.PrimType, error) {
	if qf.FunctionFunc == nil {
		return false, filtervisitor.Invalid, nil
	}
	t, err := qf.FunctionFunc(functionName, qf, args)
	return true, t, err
}

// TranslateRestriction implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) TranslateRestriction(r *fpb.Restriction) (bool, *fpb.Restriction) {
	if qf.RestrictionTranslate == nil {
		return false, nil
	}
	return true, qf.RestrictionTranslate(r)
}

// HandleDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) HandleDisjunction(value []string) (bool, safesql.TrustedSQLString, error) {
	if qf.DisjunctionFunc == nil {
		return false, safesql.New(""), errors.New("handling disjunction is not implemented")
	}
	return qf.DisjunctionFunc(value)
}

// Dialect implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) Dialect() queryfilterbase.Dialect {
	if qf.Q == nil {
		return noopDialect{}
	}
	return qf.Q.Dialect()
}

// GetTextSearchColumns implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) GetTextSearchColumns() ([]safesql.TrustedSQLString, error) {
	if len(qf.TextSearchColumns) == 0 {
		return nil, errors.New("full text search column is not implemented")
	}
	return qf.TextSearchColumns, nil
}

// GetExpOptions implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) GetExpOptions() *queryfilterbase.ExpOptions {
	if qf.filterBaseExpOptions == nil {
		qf.filterBaseExpOptions = &queryfilterbase.ExpOptions{}
	}

	for _, opt := range qf.FilterBaseOptions {
		opt(qf.filterBaseExpOptions)
	}

	return qf.filterBaseExpOptions
}

func (qf *Visitor) appendBinding(v any) {
	if qf.Q == nil {
		return
	}
	qf.AppendTrustedSQL(qf.Q.Bind(v))
}

// AppendBindingTimestamp implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingTimestamp(t time.Time) {
	qf.appendBinding(t)
}

// AppendBindingString implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingString(s string) {
	qf.appendBinding(s)
}

// AppendBindingStringArray implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingStringArray(s []string) {
	qf.appendBinding(s)
}

// AppendBindingByteArray implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingByteArray(b []byte) {
	qf.appendBinding(b)
}

// AppendBindingInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingInt64(i int64) {
	qf.appendBinding(i)
}

// AppendBindingUInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingUInt64(i uint64) {
	qf.appendBinding(i)
}

// AppendBindingFloat64 implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingFloat64(f float64) {
	qf.appendBinding(f)
}

// AppendBindingBool implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingBool(b bool) {
	qf.appendBinding(b)
}

// AppendBindingEnum implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendBindingEnum(e string) {
	qf.appendBinding(e)
}

// AppendTrustedSQL implements queryfilterbase.QueryFilterVisitor
func (qf *Visitor) AppendTrustedSQL(sql safesql.TrustedSQLString) {
	if len(qf.captureStack) > 0 {
		top := len(qf.captureStack) - 1
		qf.captureStack[top] = safesql.Concat(qf.captureStack[top], sql)
		return
	}
	if qf.Q != nil {
		qf.Q.AppendSQL(sql)
	}
}

func (qf *Visitor) SetIsRHS(isRHS bool) {
	qf.isRHS = isRHS
}

func (qf *Visitor) GetIsRHS() bool {
	return qf.isRHS
}

func (qf *Visitor) SetIsHAS(isHAS bool) {
	qf.isHAS = isHAS
}

func (qf *Visitor) GetIsHAS() bool {
	return qf.isHAS
}

// CaptureSQL captures TrustedSQLString fragments appended during fn execution.
func (qf *Visitor) CaptureSQL(fn func() error) (safesql.TrustedSQLString, error) {
	qf.captureStack = append(qf.captureStack, safesql.EmptyString())
	top := len(qf.captureStack) - 1
	defer func() {
		qf.captureStack = qf.captureStack[:top]
	}()
	err := fn()
	captured := qf.captureStack[top]
	if err != nil {
		return safesql.EmptyString(), err
	}
	return captured, nil
}

func (qf *Visitor) HandleTextSearch(col safesql.TrustedSQLString, val string) (safesql.TrustedSQLString, error) {
	if val == "" {
		return safesql.EmptyString(), errors.New("search query cannot be empty")
	}
	paramSQL, err := qf.CaptureSQL(func() error {
		qf.AppendBindingString(val)
		return nil
	})
	if err != nil {
		return safesql.EmptyString(), err
	}

	return qf.Dialect().TextSearch(col, paramSQL), nil
}
