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
// Package predicate provides a convenience helper that constructs a
// filter_expression.Expression containing the conjunction of one or more
// equality conditions.
package predicate

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	fpb "github.com/google/xmc/api/filter"
)

// Predicate represents one term in an Expression. It is implemented by versions
// for specific types, because the filtervisitor class that consumes these
// Expressions does type compatibility checking.
type Predicate interface {
	expr() *fpb.Expression
}

// String defines a "field = value" term for a string value.
type String struct {
	// Field name for the predicate.
	Field string
	// The value of the predicate.
	Value string
	// The comparator to use. Optional, defaults to equal.
	Comparator *fpb.Restriction_Comparator
}

// Bool defines a "field" or "NOT field" term for a bool-valued field.
type Bool struct {
	// Field name for the predicate.
	Field string
	// The value of the predicate.
	Value bool
}

// Int64 defines a "field = value" term for an int64 value.
type Int64 struct {
	// Field name for the predicate.
	Field string
	// The value of the predicate.
	Value int64
	// The comparator to use. Optional, defaults to equal.
	Comparator *fpb.Restriction_Comparator
}

func (s String) expr() *fpb.Expression {
	c := fpb.Restriction_EQUALS
	if s.Comparator != nil {
		c = *s.Comparator
	}
	return fpb.Expression_builder{Restriction: fpb.Restriction_builder{
		Comparator: c,
		Lhs:        fpb.Expression_builder{Value: proto.String(s.Field)}.Build(),
		Rhs:        fpb.Expression_builder{Phrase: fpb.Phrase_builder{Values: []string{s.Value}}.Build()}.Build(),
	}.Build()}.Build()
}

func (b Bool) expr() *fpb.Expression {
	if b.Value {
		// This represents a unary restriction: e.g., <field> (equivalent to <field> = true).
		return fpb.Expression_builder{Restriction: fpb.Restriction_builder{
			Comparator: fpb.Restriction_GLOBAL,
			Rhs:        fpb.Expression_builder{Value: proto.String(b.Field)}.Build(),
		}.Build()}.Build()
	}
	// This represents a false unary restriction: e.g., NOT <field> (equivalent to <field> = false).
	return fpb.Expression_builder{Negation: fpb.Expression_builder{Restriction: fpb.Restriction_builder{
		Comparator: fpb.Restriction_GLOBAL,
		Rhs:        fpb.Expression_builder{Value: proto.String(b.Field)}.Build(),
	}.Build()}.Build()}.Build()
}

func (i Int64) expr() *fpb.Expression {
	c := fpb.Restriction_EQUALS
	if i.Comparator != nil {
		c = *i.Comparator
	}
	return fpb.Expression_builder{Restriction: fpb.Restriction_builder{
		Comparator: c,
		Lhs:        fpb.Expression_builder{Value: proto.String(i.Field)}.Build(),
		Rhs:        fpb.Expression_builder{Value: proto.String(fmt.Sprintf("%v", i.Value))}.Build(),
	}.Build()}.Build()
}

// Conjunction creates a filter Expression as the AND of the given predicates.
func Conjunction(preds ...Predicate) *fpb.Expression {
	el := make([]*fpb.Expression, len(preds))
	for idx, pred := range preds {
		el[idx] = pred.expr()
	}
	if len(preds) == 1 {
		// Avoid wrapping a single condition in an unneeded Conjunction.
		return el[0]
	}
	return fpb.Expression_builder{Conjunction: fpb.ExpressionList_builder{Expressions: el}.Build()}.Build()
}

// Disjunction creates a filter Expression as the OR of the given predicates.
func Disjunction(preds ...Predicate) *fpb.Expression {
	el := make([]*fpb.Expression, len(preds))
	for idx, pred := range preds {
		el[idx] = pred.expr()
	}
	if len(preds) == 1 {
		// Avoid wrapping a single condition in an unneeded Disjunction.
		return el[0]
	}
	return fpb.Expression_builder{Disjunction: fpb.ExpressionList_builder{Expressions: el}.Build()}.Build()
}
