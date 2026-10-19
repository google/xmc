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

	"cloud.google.com/go/spanner"
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"

	fpb "xmanager_cloud/api/filter"
)

// MemberFunc represents a callback that maps a dynamic query member field path to a database representation.
type MemberFunc func([]string) (safesql.TrustedSQLString, filtervisitor.PrimType, error)

// FunctionFunc is the callback used to process function calls when augmenting a query with a filter.
// The filtervisitor.PrimType returned is the primitive type returned by the function (filtervisitor.Invalid on error).
type FunctionFunc func(functionName string, s *Spanner, args []*fpb.Expression) (filtervisitor.PrimType, error)

// PostgresFunctionFunc is the callback used to process function calls when augmenting a Postgres query with a filter.
// The filtervisitor.PrimType returned is the primitive type returned by the function (filtervisitor.Invalid on error).
type PostgresFunctionFunc func(functionName string, p *Postgres, args []*fpb.Expression) (filtervisitor.PrimType, error)

// PostgresStatement represents a parameterized PostgreSQL query with positional arguments ($1, $2, ...).
type PostgresStatement struct {
	SQL  string
	Args []any
}

// NewPostgresStatement returns a PostgresStatement initialized with the given SQL string.
func NewPostgresStatement(sql string) PostgresStatement {
	return PostgresStatement{SQL: sql}
}

var (
	_ queryfilterbase.QueryFilterVisitor = (*Spanner)(nil)
	_ queryfilterbase.QueryFilterVisitor = (*Postgres)(nil)
)

type visitorState struct {
	lastType             filtervisitor.PrimType
	filterBaseExpOptions *queryfilterbase.ExpOptions
	isRHS                bool
	isHAS                bool
	captureStack         []safesql.TrustedSQLString
}

// SetLastType implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) SetLastType(t filtervisitor.PrimType) {
	s.lastType = t
}

// GetLastType implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) GetLastType() filtervisitor.PrimType {
	return s.lastType
}

// SetIsRHS implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) SetIsRHS(isRHS bool) {
	s.isRHS = isRHS
}

// GetIsRHS implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) GetIsRHS() bool {
	return s.isRHS
}

// SetIsHAS implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) SetIsHAS(isHAS bool) {
	s.isHAS = isHAS
}

// GetIsHAS implements queryfilterbase.QueryFilterVisitor
func (s *visitorState) GetIsHAS() bool {
	return s.isHAS
}

func (s *visitorState) appendCapturedSQL(sql safesql.TrustedSQLString) bool {
	if len(s.captureStack) == 0 {
		return false
	}
	top := len(s.captureStack) - 1
	s.captureStack[top] = safesql.Concat(s.captureStack[top], sql)
	return true
}

// CaptureSQL captures TrustedSQLString fragments appended during fn execution.
func (s *visitorState) CaptureSQL(fn func() error) (safesql.TrustedSQLString, error) {
	s.captureStack = append(s.captureStack, safesql.EmptyString())
	top := len(s.captureStack) - 1
	defer func() {
		s.captureStack = s.captureStack[:top]
	}()
	err := fn()
	captured := s.captureStack[top]
	if err != nil {
		return safesql.EmptyString(), err
	}
	return captured, nil
}

func (s *visitorState) resolveExpOptions(opts []FilterBaseOption) *queryfilterbase.ExpOptions {
	if s.filterBaseExpOptions == nil {
		s.filterBaseExpOptions = &queryfilterbase.ExpOptions{}
	}
	if len(opts) > 0 {
		holder := Spanner{visitorState: *s}
		for _, opt := range opts {
			opt(&holder)
		}
		s.filterBaseExpOptions = holder.filterBaseExpOptions
	}
	return s.filterBaseExpOptions
}

func handleMember(fn MemberFunc, path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
	if fn == nil {
		return safesql.New(""), filtervisitor.Invalid, errors.New("handling member is not implemented")
	}
	return fn(path)
}

func translateRestriction(fn RestrictionTranslate, r *fpb.Restriction) (bool, *fpb.Restriction) {
	if fn == nil {
		return false, nil
	}
	return true, fn(r)
}

func handleDisjunction(fn DisjunctionFunc, value []string) (bool, safesql.TrustedSQLString, error) {
	if fn == nil {
		return false, safesql.New(""), errors.New("handling disjunction is not implemented")
	}
	return fn(value)
}

func resolveTextSearchColumns(cols []safesql.TrustedSQLString) ([]safesql.TrustedSQLString, error) {
	if len(cols) == 0 {
		return nil, errors.New("full text search column is not implemented")
	}
	return cols, nil
}

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

	// An optional function which rewrites restrictions.
	RestrictionTranslate RestrictionTranslate

	// TextSearchColumns is the list of columns to use for text search in spanner.
	// See:
	// (internal URL)
	TextSearchColumns []safesql.TrustedSQLString

	// FilterBaseOptions are options for the queryfilterbase.
	FilterBaseOptions []FilterBaseOption

	visitorState
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

// HandleMember implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) HandleMember(path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
	return handleMember(qf.MemberFunc, path)
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
	return translateRestriction(qf.RestrictionTranslate, r)
}

// HandleDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) HandleDisjunction(value []string) (bool, safesql.TrustedSQLString, error) {
	return handleDisjunction(qf.DisjunctionFunc, value)
}

// FormatDisjunctionPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) FormatDisjunctionPredicate(colSQL, paramSQL safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), colSQL, safesql.New(" IN UNNEST ("), paramSQL, safesql.New("))"))
}

// FormatArrayHasPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) FormatArrayHasPredicate(lhsSQL, rhsSQL safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), rhsSQL, safesql.New(" IN "), lhsSQL, safesql.New(")"))
}

// FormatRegexpPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) FormatRegexpPredicate(lhsSQL, rhsSQL safesql.TrustedSQLString, negated bool) safesql.TrustedSQLString {
	match := safesql.Concat(safesql.New("(REGEXP_CONTAINS("), lhsSQL, safesql.New(" , "), rhsSQL, safesql.New("))"))
	if negated {
		return safesql.Concat(safesql.New("(NOT "), match, safesql.New(")"))
	}
	return match
}

// GetTextSearchColumns implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetTextSearchColumns() ([]safesql.TrustedSQLString, error) {
	return resolveTextSearchColumns(qf.TextSearchColumns)
}

// GetExpOptions implements queryfilterbase.QueryFilterVisitor
func (qf *Spanner) GetExpOptions() *queryfilterbase.ExpOptions {
	return qf.resolveExpOptions(qf.FilterBaseOptions)
}

func (qf *Spanner) appendBinding(v any) {
	if qf.Q == nil {
		return
	}
	index := atomic.AddUint64(&qf.count, 1) - 1

	// Append the binding name to the query.
	qf.AppendTrustedSQL(safesql.NewBindingParam("@fb", index))

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
	if qf.appendCapturedSQL(sql) {
		return
	}
	if qf.Q != nil {
		qf.Q.SQL = qf.Q.SQL + sql.String()
	}
}

func (qf *Spanner) HandleTextSearch(col safesql.TrustedSQLString, val string) (safesql.TrustedSQLString, error) {
	paramSQL, err := qf.CaptureSQL(func() error {
		qf.AppendBindingString(val)
		return nil
	})
	if err != nil {
		return safesql.EmptyString(), err
	}

	return safesql.Concat(safesql.New("SEARCH("), col, safesql.New(", "), paramSQL, safesql.New(")")), nil
}

// Postgres appends a filter expression to a PostgresStatement using the provided MemberFunc for appending member accesses.
// Implements the queryfilterbase.QueryFilterVisitor interface. This struct is not threadsafe, as it modifies the query.
type Postgres struct {
	MemberFunc   MemberFunc
	FunctionFunc PostgresFunctionFunc

	// Whether to convert a query of the type `a = 1 or a = 2 or a = 3` to `a = ANY($N)` to optimize query compilation.
	OptimizeDisjunction bool
	// Provides direct access to a member before appending to the query.
	DisjunctionFunc DisjunctionFunc

	Q *PostgresStatement

	// An optional function which rewrites restrictions.
	RestrictionTranslate RestrictionTranslate

	// TextSearchColumns is the list of TSVECTOR columns to use for full-text search in PostgreSQL.
	TextSearchColumns []safesql.TrustedSQLString

	// FilterBaseOptions are options for the queryfilterbase.
	FilterBaseOptions []FilterBaseOption

	visitorState
}

// AppendFilters augments the given Postgres query with a predicate expression derived from the parsed filter expression.
func (qf *Postgres) AppendFilters(e *fpb.Expression) error {
	return queryfilterbase.AppendFilters(qf, e)
}

// FormatDisjunctionPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) FormatDisjunctionPredicate(colSQL, paramSQL safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), colSQL, safesql.New(" = ANY("), paramSQL, safesql.New("))"))
}

// ConjunctionExp implements filtervisitor.FilterVisitor
func (qf *Postgres) ConjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.ConjunctionExp(qf, e)
}

// DisjunctionExp implements filtervisitor.FilterVisitor
func (qf *Postgres) DisjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.DisjunctionExp(qf, e)
}

// NegationExp implements filtervisitor.FilterVisitor
func (qf *Postgres) NegationExp(e *fpb.Expression) (filtervisitor.PrimType, error) {
	return queryfilterbase.NegationExp(qf, e)
}

// FormatArrayHasPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) FormatArrayHasPredicate(lhsSQL, rhsSQL safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), rhsSQL, safesql.New(" = ANY("), lhsSQL, safesql.New("))"))
}

// RestrictionExp implements filtervisitor.FilterVisitor
func (qf *Postgres) RestrictionExp(e *fpb.Restriction) (filtervisitor.PrimType, error) {
	return queryfilterbase.RestrictionExp(qf, e)
}

// MemberExp implements filtervisitor.FilterVisitor
func (qf *Postgres) MemberExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	return queryfilterbase.MemberExp(qf, e)
}

// FormatRegexpPredicate implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) FormatRegexpPredicate(lhsSQL, rhsSQL safesql.TrustedSQLString, negated bool) safesql.TrustedSQLString {
	match := safesql.Concat(safesql.New("("), lhsSQL, safesql.New(" ~ "), rhsSQL, safesql.New(")"))
	if negated {
		return safesql.Concat(safesql.New("(NOT "), match, safesql.New(")"))
	}
	return match
}

// FunctionExp implements filtervisitor.FilterVisitor
func (qf *Postgres) FunctionExp(e *fpb.Function) (filtervisitor.PrimType, error) {
	return queryfilterbase.FunctionExp(qf, e)
}

// ValueExp implements filtervisitor.FilterVisitor
func (qf *Postgres) ValueExp(e string) (filtervisitor.PrimType, error) {
	return queryfilterbase.ValueExp(qf, e)
}

// PhraseExp implements filtervisitor.FilterVisitor
func (qf *Postgres) PhraseExp(e *fpb.Phrase) (filtervisitor.PrimType, error) {
	return queryfilterbase.PhraseExp(qf, e)
}

// HandleTextSearch implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) HandleTextSearch(col safesql.TrustedSQLString, val string) (safesql.TrustedSQLString, error) {
	if val == "" {
		return safesql.EmptyString(), errors.New("full text search value cannot be empty")
	}
	paramSQL, err := qf.CaptureSQL(func() error {
		qf.AppendBindingString(val)
		return nil
	})
	if err != nil {
		return safesql.EmptyString(), err
	}

	return safesql.Concat(col, safesql.New(" @@ plainto_tsquery('simple', "), paramSQL, safesql.New(")")), nil
}

// GetOptimizeDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) GetOptimizeDisjunction() bool {
	return qf.OptimizeDisjunction
}

// HandleMember implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) HandleMember(path []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
	return handleMember(qf.MemberFunc, path)
}

// HandleFunction implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) HandleFunction(functionName string, args []*fpb.Expression) (bool, filtervisitor.PrimType, error) {
	if qf.FunctionFunc == nil {
		return false, filtervisitor.Invalid, nil
	}
	t, err := qf.FunctionFunc(functionName, qf, args)
	return true, t, err
}

func (qf *Postgres) appendBinding(v any) {
	if qf.Q == nil {
		return
	}
	qf.Q.Args = append(qf.Q.Args, v)
	index := uint64(len(qf.Q.Args))
	qf.AppendTrustedSQL(safesql.NewBindingParam("$", index))
}

// TranslateRestriction implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) TranslateRestriction(r *fpb.Restriction) (bool, *fpb.Restriction) {
	return translateRestriction(qf.RestrictionTranslate, r)
}

// HandleDisjunction implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) HandleDisjunction(value []string) (bool, safesql.TrustedSQLString, error) {
	return handleDisjunction(qf.DisjunctionFunc, value)
}

// GetTextSearchColumns implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) GetTextSearchColumns() ([]safesql.TrustedSQLString, error) {
	return resolveTextSearchColumns(qf.TextSearchColumns)
}

// GetExpOptions implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) GetExpOptions() *queryfilterbase.ExpOptions {
	return qf.resolveExpOptions(qf.FilterBaseOptions)
}

// AppendBindingTimestamp implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingTimestamp(t time.Time) {
	qf.appendBinding(t)
}

// AppendBindingString implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingString(s string) {
	qf.appendBinding(s)
}

// AppendBindingStringArray implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingStringArray(s []string) {
	qf.appendBinding(s)
}

// AppendBindingByteArray implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingByteArray(b []byte) {
	qf.appendBinding(b)
}

// AppendTrustedSQL implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendTrustedSQL(sql safesql.TrustedSQLString) {
	if qf.appendCapturedSQL(sql) {
		return
	}
	if qf.Q != nil {
		qf.Q.SQL = qf.Q.SQL + sql.String()
	}
}

// AppendBindingInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingInt64(i int64) {
	qf.appendBinding(i)
}

// AppendBindingUInt64 implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingUInt64(i uint64) {
	qf.appendBinding(i)
}

// AppendBindingFloat64 implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingFloat64(f float64) {
	qf.appendBinding(f)
}

// AppendBindingBool implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingBool(b bool) {
	qf.appendBinding(b)
}

// AppendBindingEnum implements queryfilterbase.QueryFilterVisitor
func (qf *Postgres) AppendBindingEnum(e string) {
	qf.appendBinding(e)
}
