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
// Package normalizer provides functions for normalizing user-provided
// query strings to a standardized format.
package normalizer

import (
	"fmt"
	"strings"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
)

type normalizerState struct {
	schema          querytools.TableView
	lastToken       Token
	insideFieldExpr bool
	activeType      filtervisitor.PrimType
	sb              strings.Builder
}

func promoteLogicalKeyword(tok Token) Token {
	if tok.Type != tokenIdent {
		return tok
	}
	switch strings.ToUpper(tok.Literal) {
	case "AND", "OR", "NOT":
		tok.Raw = strings.ToUpper(tok.Raw)
		tok.Type = tokenKeyword
	}
	return tok
}

func isArrayNegationPattern(schema querytools.TableView, i int, tokens []Token) bool {
	if i+2 >= len(tokens) || tokens[i+1].Type != tokenIdent || tokens[i+2].Type != tokenOp || tokens[i+2].Literal != ":" {
		return false
	}
	return findInTable(schema, tokens[i+1].Literal).Name == filtervisitor.Array
}

func (s *normalizerState) handleOpToken(tok Token, i int, tokens []Token) Token {
	if tok.Literal == "-" {
		s.insideFieldExpr = false
		if isArrayNegationPattern(s.schema, i, tokens) {
			s.sb.WriteString("NOT")
			tok.Type = tokenKeyword
			return tok
		}
		s.sb.WriteString(tok.Raw)
		return tok
	}

	s.insideFieldExpr = true
	if s.lastToken.Type == tokenIdent || s.lastToken.Type == tokenString {
		s.activeType = findInTable(s.schema, s.lastToken.Literal)
		nextIsLParen := i+1 < len(tokens) && tokens[i+1].Type == tokenLParen
		if tok.Literal == ":" && s.activeType.Name != filtervisitor.Array && !nextIsLParen {
			s.sb.WriteString("=")
			return tok
		}
	}
	s.sb.WriteString(tok.Raw)
	return tok
}

func (s *normalizerState) processToken(i int, tokens []Token) {
	tok := promoteLogicalKeyword(tokens[i])
	if i > 0 && shouldAddSpace(s.lastToken, tok) {
		s.sb.WriteString(" ")
	}

	switch tok.Type {
	case tokenOp:
		tok = s.handleOpToken(tok, i, tokens)
	case tokenKeyword, tokenLParen, tokenRParen:
		s.insideFieldExpr = false
		s.sb.WriteString(tok.Raw)
	case tokenIdent, tokenString, tokenNumber:
		s.insideFieldExpr = handleIdentOrValueToken(tok, s.insideFieldExpr, s.activeType, i, tokens, &s.sb)
	default:
		s.sb.WriteString(tok.Raw)
	}
	s.lastToken = tok
}

// Normalize takes a semi-compliant AIP-160 string and enforces quoting rules
// based on the provided schema.
func Normalize(input string, schema querytools.TableView) (string, error) {
	tokens := Tokenize(input)
	state := normalizerState{schema: schema}
	for i := range tokens {
		state.processToken(i, tokens)
	}
	return state.sb.String(), nil
}

func fixValue(t Token, primType filtervisitor.PrimType) string {
	// If the type is an array, unwrap it until we get a primitive type.
	for primType.Name == filtervisitor.Array {
		primType = *primType.SubType
	}

	if primType.Compatible(filtervisitor.String) {
		if t.Type == tokenString {
			return t.Raw // Already quoted
		}
		return fmt.Sprintf("%q", t.Literal)
	}
	if primType.Compatible(filtervisitor.Integer) {
		if t.Type == tokenString {
			// If an integer value was quoted, unquote it.
			return t.Literal
		}
		return t.Raw
	}
	if primType.Compatible(filtervisitor.Boolean) {
		return t.Raw
	}
	if t.Type == tokenIdent {
		return fmt.Sprintf("%q", t.Literal)
	}
	return t.Raw
}

func shouldAddSpace(prev, curr Token) bool {
	// Don't space inside parens: ( expression ) -> (expression)
	if prev.Type == tokenLParen || curr.Type == tokenRParen {
		return false
	}

	// If previous token was NOT, AND, or OR, always add a space
	if prev.Type == tokenKeyword {
		return true
	}

	// Keep tight spacing around operators like '=', ':', and ','.
	if prev.Type == tokenOp || curr.Type == tokenOp {
		if curr.Type == tokenOp && curr.Literal == "-" {
			// If current is unary -, require space if prev is not an operator or lparen
			return prev.Type != tokenOp && prev.Type != tokenLParen
		}
		if prev.Type == tokenOp && prev.Literal == "-" {
			return false // no space after unary -
		}
		return false
	}

	// Function calls should be tight: has(tags)
	if curr.Type == tokenLParen && prev.Type == tokenIdent {
		return false
	}

	// Default to adding a space.
	return true
}

func findInTable(table querytools.TableView, name string) filtervisitor.PrimType {
	if member, ok := table.Fields[querytools.TableField(name)]; ok {
		return member.Type
	}

	// Default to string if the field is not found.
	return filtervisitor.String
}

func handleIdentOrValueToken(token Token, insideFieldExpr bool, activeType filtervisitor.PrimType, i int, tokens []Token, sb *strings.Builder) bool {
	if insideFieldExpr {
		// Token is a value in a field expression, fix its quoting based on type.
		val := fixValue(token, activeType)
		sb.WriteString(val)
		return false
	}

	// Token is not a value, it could be a field name or free text.
	var nextIsStructural bool
	if i+1 < len(tokens) {
		tType := tokens[i+1].Type
		nextIsStructural = (tType == tokenOp || tType == tokenLParen)
	}

	if nextIsStructural {
		// If the next token is an operator or parenthesis, this token is a field or function name.
		sb.WriteString(token.Raw)
	} else {
		// Otherwise, it is free-floating text and should be quoted as a global search term.
		if token.Type == tokenIdent {
			sb.WriteString(fmt.Sprintf("%q", token.Literal))
		} else {
			sb.WriteString(token.Raw)
		}
	}
	return false
}
