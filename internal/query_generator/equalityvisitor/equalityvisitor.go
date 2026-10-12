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
// Package equalityvisitor walks a filter expression, capturing members that
// have an equality statement.
package equalityvisitor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/logging/logging"

	fpb "xmanager_cloud/api/filter/filter_expression_go_proto"
)

// FunctionFunc is the callback used to process function calls when parsing an expression.
// The filtervisitor.PrimType returned is the primitive type returned by the function (filtervisitor.Invalid on error).
type FunctionFunc func(functionName string, ev *EqualityVisitor, args []*fpb.Expression) (filtervisitor.PrimType, error)

// MemberFunc is the callback used to process member accesses when parsing an expression with a member.
// The bool argument indicates whether the member is being evaluated as part of an equality statement.
// The filtervisitor.PrimType returned is the primitive type of the member being accessed (filtervisitor.Invalid on error).
type MemberFunc func([]string, bool) (filtervisitor.PrimType, error)

// EqualityVisitor is an implementation of filtervisitor.FilterVisitor that
// gathers the list of members that are used in equality filters.
// EqualityVisitor does not check for correctness of the expression it walks.
type EqualityVisitor struct {
	visitFromEqual bool
	FunctionFunc   FunctionFunc
	MemberFunc     MemberFunc
}

// ConjunctionExp handles expressions with the conjunction type
func (ev *EqualityVisitor) ConjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	ev.visitFromEqual = false
	if len(e.GetExpressions()) == 0 {
		return filtervisitor.Invalid, errors.New("empty expression list")
	}
	var err error
	for i, expr := range e.GetExpressions() {
		ev.visitFromEqual = false
		_, err = filtervisitor.Visit(ev, expr)
		if err != nil {
			err = propagateError(err, fmt.Sprintf("element %v", i))
			break
		}
	}
	return filtervisitor.Invalid, err
}

// propagateError augments the given error with context for better diagnostics.
func propagateError(err error, ctx string) error {
	if err == nil {
		return err
	}
	return fmt.Errorf("%v: %v", ctx, err)
}

// DisjunctionExp handles expressions with the disjunction type
func (ev *EqualityVisitor) DisjunctionExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	// We don't need to process the inner expressions in a disjunction because
	// in the event of an OR we still need to append filters for page tokens.
	ev.visitFromEqual = false
	return filtervisitor.Invalid, nil
}

// NegationExp handles expressions with the negation type
func (ev *EqualityVisitor) NegationExp(e *fpb.Expression) (filtervisitor.PrimType, error) {
	// We don't need to process the inner expressions in a disjunction because
	// in the event of an OR we still need to append filters for page tokens.
	ev.visitFromEqual = false
	return filtervisitor.Invalid, nil
}

// RestrictionExp handles expressions with the restriction type
func (ev *EqualityVisitor) RestrictionExp(e *fpb.Restriction) (filtervisitor.PrimType, error) {
	// all comparators other than eq
	if e.GetComparator() != fpb.Restriction_EQUALS {
		return filtervisitor.Invalid, nil
	}
	// check if lhs or rhs is a member.
	ev.visitFromEqual = true
	if _, err := filtervisitor.Visit(ev, e.GetLhs()); err != nil {
		logging.Info(context.Background(), "in lhs visit", "lhs", e.GetLhs(), "visitFromEqual", ev.visitFromEqual)
		return filtervisitor.Invalid, errors.New("unexpected left-hand operand with global comparator")
	}
	ev.visitFromEqual = true
	if _, err := filtervisitor.Visit(ev, e.GetRhs()); err != nil {
		logging.Info(context.Background(), "in rhs visit", "rhs", e.GetRhs(), "visitFromEqual", ev.visitFromEqual)

		return filtervisitor.Invalid, errors.New("unexpected left-hand operand with global comparator")
	}
	return filtervisitor.Invalid, nil
}

// MemberExp handles expressions with the member type
func (ev *EqualityVisitor) MemberExp(e *fpb.ExpressionList) (filtervisitor.PrimType, error) {
	ml := make([]string, 0, len(e.GetExpressions()))
	for _, x := range e.GetExpressions() {
		var field string
		switch x.WhichExpression() {
		case fpb.Expression_Value_case:
			field = x.GetValue()
		default:
			return filtervisitor.Invalid, errors.New("saw non-Value in member access")
		}

		ml = append(ml, field)
	}

	ft, err := ev.MemberFunc(ml, ev.visitFromEqual)
	if err != nil {
		return filtervisitor.Invalid, propagateError(err, "value")
	}
	if ft.Equals(filtervisitor.Invalid) {
		if !ev.visitFromEqual {
			return filtervisitor.Invalid, nil
		}
		logging.Error(context.Background(), "MemberFunc should only return filtervisitor.Invalid with an error", "input", ml)
		return filtervisitor.Invalid, errors.New("value: member has invalid type")
	}
	return ft, nil
}

// FunctionExp handles expressions with the function type
func (ev *EqualityVisitor) FunctionExp(e *fpb.Function) (filtervisitor.PrimType, error) {
	// Functions need to be executed to ensure that we capture any equality statements in them.
	// See if the user has provided us with a callback for processing the fpb.Function.
	// If not, then functions are not supported.
	if ev.FunctionFunc == nil {
		logging.Error(context.Background(), "Saw Function, but no FunctionFunc callback was provided", "function", e)
		return filtervisitor.Invalid, errors.New("functions are not supported")
	}
	// If the user provided us with a FunctionFunc callback, then ensure the function has
	// a constant name, and call the FunctionFunc callback with the contents of the fpb.Function.
	switch e.GetFunction().WhichExpression() {
	case fpb.Expression_Value_case:
		return ev.FunctionFunc(e.GetFunction().GetValue(), ev, e.GetArgs())
	default:
		return filtervisitor.Invalid, nil
	}
}

// ValueExp handles expressions with the value type
func (ev *EqualityVisitor) ValueExp(e string) (filtervisitor.PrimType, error) {
	if (strings.HasPrefix(e, "\"") && strings.HasSuffix(e, "\"")) || (strings.HasPrefix(e, "'") && strings.HasSuffix(e, "'")) {
		ev.visitFromEqual = false
		return filtervisitor.String, nil
	}
	if i, err := strconv.ParseInt(e, 10, 0); err == nil {
		if i < 0 {
			ev.visitFromEqual = false
			return filtervisitor.Integer, nil
		}
		ev.visitFromEqual = false
		return filtervisitor.Unsigned, nil
	}
	// Sanitize float literal
	if _, err := strconv.ParseFloat(e, 0); err == nil {
		ev.visitFromEqual = false
		return filtervisitor.Float, nil
	}
	// Sanitize bool literal
	if _, err := strconv.ParseBool(e); err == nil {
		ev.visitFromEqual = false
		return filtervisitor.Boolean, nil
	}
	ft, err := ev.MemberFunc([]string{e}, ev.visitFromEqual)
	if err != nil {
		return filtervisitor.Invalid, propagateError(err, "value")
	}
	if ft.Equals(filtervisitor.Invalid) {
		return filtervisitor.Invalid, errors.New("member: invalid primitive type")
	}
	return ft, nil
}

// PhraseExp handles expressions with the phrase type
func (ev *EqualityVisitor) PhraseExp(e *fpb.Phrase) (filtervisitor.PrimType, error) {
	ev.visitFromEqual = false
	return filtervisitor.Invalid, nil
}
