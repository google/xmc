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

package filtervisitor

import (
	"errors"
	fpb "github.com/google/xmc/api/filter"
)

type PrimType struct {
	Name    string
	SubType *PrimType
}

const Array = "ARRAY"

var (
	Invalid        = PrimType{Name: "INVALID"}
	Integer        = PrimType{Name: "INTEGER"}
	String         = PrimType{Name: "STRING"}
	Bytes          = PrimType{Name: "BYTES"}
	Timestamp      = PrimType{Name: "TIMESTAMP"}
	Boolean        = PrimType{Name: "BOOLEAN"}
	Unsigned       = PrimType{Name: "UNSIGNED"}
	Float          = PrimType{Name: "FLOAT"}
	Enum           = PrimType{Name: "ENUM"}
	ProtoTimestamp = PrimType{Name: "PROTO_TIMESTAMP"}
	Date           = PrimType{Name: "DATE"}
	StrConst       = PrimType{Name: "STR_CONST"}
)

func CustomType(name string) PrimType {
	return PrimType{Name: name}
}

func ArrayType(elem *PrimType) PrimType {
	return PrimType{Name: Array, SubType: elem}
}

func (p PrimType) Equals(other PrimType) bool {
	if p.Name != other.Name {
		return false
	}
	if p.SubType == nil && other.SubType == nil {
		return true
	}
	if p.SubType == nil || other.SubType == nil {
		return false
	}
	return p.SubType.Equals(*other.SubType)
}

func (p PrimType) Compatible(other PrimType) bool {
	if p.Equals(other) {
		return true
	}
	pName := p.Name
	oName := other.Name

	// Numeric family
	isNumeric := func(n string) bool { return n == "INTEGER" || n == "UNSIGNED" || n == "FLOAT" }
	if isNumeric(pName) && isNumeric(oName) {
		return true
	}

	// String/Enum family
	isStringy := func(n string) bool { return n == "STRING" || n == "ENUM" || n == "STR_CONST" }
	if isStringy(pName) && isStringy(oName) {
		return true
	}

	// Temporal family
	isTemporal := func(n string) bool { return n == "TIMESTAMP" || n == "DATE" || n == "PROTO_TIMESTAMP" }
	if isTemporal(pName) && isTemporal(oName) {
		return true
	}

	return false
}

type FilterVisitor interface {
	ConjunctionExp(e *fpb.ExpressionList) (PrimType, error)
	DisjunctionExp(e *fpb.ExpressionList) (PrimType, error)
	NegationExp(e *fpb.Expression) (PrimType, error)
	RestrictionExp(e *fpb.Restriction) (PrimType, error)
	MemberExp(e *fpb.ExpressionList) (PrimType, error)
	FunctionExp(e *fpb.Function) (PrimType, error)
	ValueExp(e string) (PrimType, error)
	PhraseExp(e *fpb.Phrase) (PrimType, error)
}

func Visit(visitor FilterVisitor, e *fpb.Expression) (PrimType, error) {
	if e == nil {
		return Invalid, errors.New("nil expression")
	}
	switch e.WhichExpression() {
	case fpb.Expression_Value_case:
		return visitor.ValueExp(e.GetValue())
	case fpb.Expression_Conjunction_case:
		return visitor.ConjunctionExp(e.GetConjunction())
	case fpb.Expression_Disjunction_case:
		return visitor.DisjunctionExp(e.GetDisjunction())
	case fpb.Expression_Negation_case:
		return visitor.NegationExp(e.GetNegation())
	case fpb.Expression_Restriction_case:
		return visitor.RestrictionExp(e.GetRestriction())
	case fpb.Expression_Function_case:
		return visitor.FunctionExp(e.GetFunction())
	case fpb.Expression_Phrase_case:
		return visitor.PhraseExp(e.GetPhrase())
	case fpb.Expression_Member_case:
		return visitor.MemberExp(e.GetMember())
	}
	return Invalid, errors.New("unknown expression case")
}
