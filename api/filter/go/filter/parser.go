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

package filter

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	fpb "xmanager_cloud/api/filter"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokAnd
	tokOr
	tokNot
	tokIdentifier
	tokString
	tokInt
	tokFloat
	tokBool
	tokEq
	tokNotEq
	tokLt
	tokLtEq
	tokGt
	tokGtEq
	tokHas
	tokRegexMatch
	tokRegexNotMatch
	tokLParen
	tokRParen
	tokComma
	tokMinus
)

type token struct {
	kind  tokenKind
	value string
	pos   int
}

type lexer struct {
	input []rune
	pos   int
}

func newLexer(input string) *lexer {
	return &lexer{input: []rune(input)}
}

func (l *lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *lexer) advance() rune {
	r := l.peek()
	if r != 0 {
		l.pos++
	}
	return r
}

func (l *lexer) skipWhitespace() {
	for unicode.IsSpace(l.peek()) {
		l.advance()
	}
}

func (l *lexer) scanComparisonOrOperator(r rune, startPos int) (token, bool, error) {
	switch r {
	case '(':
		l.advance()
		return token{kind: tokLParen, value: "(", pos: startPos}, true, nil
	case ')':
		l.advance()
		return token{kind: tokRParen, value: ")", pos: startPos}, true, nil
	case ',':
		l.advance()
		return token{kind: tokComma, value: ",", pos: startPos}, true, nil
	case ':':
		l.advance()
		return token{kind: tokHas, value: ":", pos: startPos}, true, nil
	case '-':
		l.advance()
		if unicode.IsDigit(l.peek()) {
			numStr := l.scanNumber("")
			return token{kind: l.deduceNumberKind("-" + numStr), value: "-" + numStr, pos: startPos}, true, nil
		}
		return token{kind: tokMinus, value: "-", pos: startPos}, true, nil
	case '=':
		return l.scanEqualsToken(startPos), true, nil
	case '!':
		return l.scanBangToken(startPos)
	case '<':
		return l.scanOrderToken(tokLt, tokLtEq, "<", "<=", startPos), true, nil
	case '>':
		return l.scanOrderToken(tokGt, tokGtEq, ">", ">=", startPos), true, nil
	case '"', '\'':
		strVal, err := l.scanString(r)
		if err != nil {
			return token{}, false, err
		}
		return token{kind: tokString, value: strVal, pos: startPos}, true, nil
	default:
		return token{}, false, nil
	}
}

func (l *lexer) scanEqualsToken(startPos int) token {
	l.advance()
	if l.peek() == '~' {
		l.advance()
		return token{kind: tokRegexMatch, value: "=~", pos: startPos}
	}
	return token{kind: tokEq, value: "=", pos: startPos}
}

func (l *lexer) scanBangToken(startPos int) (token, bool, error) {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return token{kind: tokNotEq, value: "!=", pos: startPos}, true, nil
	}
	if l.peek() == '~' {
		l.advance()
		return token{kind: tokRegexNotMatch, value: "!~", pos: startPos}, true, nil
	}
	return token{}, false, fmt.Errorf("unexpected char '!' at position %d", startPos)
}

func (l *lexer) scanOrderToken(singleKind, eqKind tokenKind, singleVal, eqVal string, startPos int) token {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return token{kind: eqKind, value: eqVal, pos: startPos}
	}
	return token{kind: singleKind, value: singleVal, pos: startPos}
}

func keywordOrIdentToken(ident string, startPos int) token {
	switch strings.ToLower(ident) {
	case "and":
		return token{kind: tokAnd, value: ident, pos: startPos}
	case "or":
		return token{kind: tokOr, value: ident, pos: startPos}
	case "not":
		return token{kind: tokNot, value: ident, pos: startPos}
	case "true":
		return token{kind: tokBool, value: "true", pos: startPos}
	case "false":
		return token{kind: tokBool, value: "false", pos: startPos}
	default:
		return token{kind: tokIdentifier, value: ident, pos: startPos}
	}
}

func (l *lexer) nextToken() (token, error) {
	l.skipWhitespace()
	r := l.peek()
	if r == 0 {
		return token{kind: tokEOF, value: "", pos: l.pos}, nil
	}

	startPos := l.pos
	if tok, ok, err := l.scanComparisonOrOperator(r, startPos); err != nil || ok {
		return tok, err
	}
	if unicode.IsDigit(r) {
		numStr := l.scanNumber("")
		return token{kind: l.deduceNumberKind(numStr), value: numStr, pos: startPos}, nil
	}
	if isAlpha(r) || r == '_' {
		return keywordOrIdentToken(l.scanIdentifier(), startPos), nil
	}
	return token{}, fmt.Errorf("unexpected character %q at position %d", r, startPos)
}

func (l *lexer) scanString(quote rune) (string, error) {
	l.advance() // consume open quote
	var sb strings.Builder
	sb.WriteRune(quote)
	for {
		r := l.peek()
		if r == 0 {
			return "", fmt.Errorf("unterminated string literal starting at position %d", l.pos)
		}
		if r == quote {
			l.advance() // consume close quote
			sb.WriteRune(quote)
			break
		}
		if r == '\\' {
			l.advance()
			next := l.advance()
			switch next {
			case 'n':
				sb.WriteRune('\n')
			case 'r':
				sb.WriteRune('\r')
			case 't':
				sb.WriteRune('\t')
			case '\\':
				sb.WriteRune('\\')
			case quote:
				sb.WriteRune(quote)
			default:
				sb.WriteRune('\\')
				if next != 0 {
					sb.WriteRune(next)
				}
			}
		} else {
			sb.WriteRune(l.advance())
		}
	}
	return sb.String(), nil
}

func (l *lexer) scanNumber(prefix string) string {
	var sb strings.Builder
	sb.WriteString(prefix)
	for {
		r := l.peek()
		if !unicode.IsDigit(r) && r != '.' && r != 'e' && r != 'E' && r != '+' && r != '-' {
			break
		}
		sb.WriteRune(l.advance())
	}
	return sb.String()
}

func (l *lexer) deduceNumberKind(s string) tokenKind {
	if strings.ContainsAny(s, ".eE") {
		return tokFloat
	}
	return tokInt
}

func (l *lexer) scanIdentifier() string {
	var sb strings.Builder
	for {
		r := l.peek()
		if !isAlphaNum(r) && r != '_' && r != '.' {
			break
		}
		sb.WriteRune(l.advance())
	}
	return sb.String()
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isAlphaNum(r rune) bool {
	return isAlpha(r) || (r >= '0' && r <= '9')
}

type parser struct {
	lex     *lexer
	tokens  []token
	curr    int
	options *fpb.ExpressionParserOptions
}

func newParser(input string, options *fpb.ExpressionParserOptions) (*parser, error) {
	l := newLexer(input)
	var tokens []token
	for {
		tok, err := l.nextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.kind == tokEOF {
			break
		}
	}
	if options == nil {
		options = &fpb.ExpressionParserOptions{}
	}
	return &parser{lex: l, tokens: tokens, options: options}, nil
}

func (p *parser) peek() token {
	if p.curr >= len(p.tokens) {
		return token{kind: tokEOF, value: ""}
	}
	return p.tokens[p.curr]
}

func (p *parser) advance() token {
	tok := p.peek()
	if tok.kind != tokEOF {
		p.curr++
	}
	return tok
}

func (p *parser) match(kind tokenKind) bool {
	if p.peek().kind == kind {
		p.advance()
		return true
	}
	return false
}

func ParseExpression(input string) (*fpb.Expression, error) {
	return ParseExpressionWithOptions(nil, input)
}

func ParseExpressionWithOptions(options *fpb.ExpressionParserOptions, input string) (*fpb.Expression, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}
	p, err := newParser(input, options)
	if err != nil {
		return nil, err
	}
	exp, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected trailing token %q at position %d", p.peek().value, p.peek().pos)
	}
	return exp, nil
}

func (p *parser) parseExpression() (*fpb.Expression, error) {
	return p.parseExplicitAnd()
}

func (p *parser) parseExplicitAnd() (*fpb.Expression, error) {
	left, err := p.parseExplicitOr()
	if err != nil {
		return nil, err
	}
	var list []*fpb.Expression
	list = append(list, left)
	for p.peek().kind == tokAnd {
		p.advance() // consume explicit AND
		right, err := p.parseExplicitOr()
		if err != nil {
			return nil, err
		}
		list = append(list, right)
	}
	if len(list) == 1 {
		return list[0], nil
	}
	return &fpb.Expression{
		Expression: &fpb.Expression_Conjunction{
			Conjunction: &fpb.ExpressionList{Expressions: list},
		},
	}, nil
}

func (p *parser) parseExplicitOr() (*fpb.Expression, error) {
	left, err := p.parseImplicitAnd()
	if err != nil {
		return nil, err
	}
	var list []*fpb.Expression
	list = append(list, left)
	for p.match(tokOr) {
		right, err := p.parseImplicitAnd()
		if err != nil {
			return nil, err
		}
		list = append(list, right)
	}
	if len(list) == 1 {
		return list[0], nil
	}
	return &fpb.Expression{
		Expression: &fpb.Expression_Disjunction{
			Disjunction: &fpb.ExpressionList{Expressions: list},
		},
	}, nil
}

func (p *parser) parseImplicitAnd() (*fpb.Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	var list []*fpb.Expression
	list = append(list, left)
	for p.isStartOfPrimary() && p.peek().kind != tokOr && p.peek().kind != tokAnd {
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		list = append(list, right)
	}
	if len(list) == 1 {
		return list[0], nil
	}
	return &fpb.Expression{
		Expression: &fpb.Expression_Conjunction{
			Conjunction: &fpb.ExpressionList{Expressions: list},
		},
	}, nil
}

func (p *parser) isStartOfPrimary() bool {
	tok := p.peek()
	return tok.kind == tokNot || tok.kind == tokMinus || tok.kind == tokIdentifier ||
		tok.kind == tokString || tok.kind == tokInt || tok.kind == tokFloat ||
		tok.kind == tokBool || tok.kind == tokLParen
}

func (p *parser) parsePrimary() (*fpb.Expression, error) {
	if p.match(tokNot) {
		sub, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &fpb.Expression{
			Expression: &fpb.Expression_Negation{Negation: sub},
		}, nil
	}
	if p.match(tokMinus) {
		sub, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &fpb.Expression{
			Expression: &fpb.Expression_Negation{Negation: sub},
		}, nil
	}
	if p.match(tokLParen) {
		sub, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if !p.match(tokRParen) {
			return nil, errors.New("missing closing parenthesis")
		}
		return sub, nil
	}
	return p.parseRestriction()
}

func isComparator(k tokenKind) bool {
	return k == tokEq || k == tokNotEq || k == tokLt || k == tokLtEq ||
		k == tokGt || k == tokGtEq || k == tokHas ||
		k == tokRegexMatch || k == tokRegexNotMatch
}

func (p *parser) comparatorFromToken(comp tokenKind) (fpb.Restriction_Comparator, error) {
	switch comp {
	case tokEq:
		return fpb.Restriction_EQUALS, nil
	case tokNotEq:
		return fpb.Restriction_NOT_EQUALS, nil
	case tokLt:
		return fpb.Restriction_LESS_THAN, nil
	case tokLtEq:
		return fpb.Restriction_LESS_EQUALS, nil
	case tokGt:
		return fpb.Restriction_GREATER_THAN, nil
	case tokGtEq:
		return fpb.Restriction_GREATER_EQUALS, nil
	case tokHas:
		return fpb.Restriction_HAS, nil
	case tokRegexMatch, tokRegexNotMatch:
		if !p.options.GetEnableRegexpMatching() {
			opStr := "=~"
			if comp == tokRegexNotMatch {
				opStr = "!~"
			}
			return fpb.Restriction_COMPARATOR_UNSPECIFIED, fmt.Errorf("regexp matching operator %s is disabled by options", opStr)
		}
		if comp == tokRegexMatch {
			return fpb.Restriction_REGEXP_MATCH, nil
		}
		return fpb.Restriction_REGEXP_NOT_MATCH, nil
	default:
		return fpb.Restriction_COMPARATOR_UNSPECIFIED, fmt.Errorf("unsupported comparator token %v", comp)
	}
}

func (p *parser) parseRestriction() (*fpb.Expression, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}

	comp := p.peek().kind
	if !isComparator(comp) {
		return &fpb.Expression{
			Expression: &fpb.Expression_Restriction{
				Restriction: &fpb.Restriction{
					Comparator: fpb.Restriction_GLOBAL,
					Rhs:        left,
				},
			},
		}, nil
	}

	p.advance() // Consume comparator
	isRegexOp := comp == tokRegexMatch || comp == tokRegexNotMatch
	var right *fpb.Expression
	if isRegexOp && p.peek().kind == tokLParen {
		right, err = p.parseExpression()
	} else {
		right, err = p.parseTerm()
	}
	if err != nil {
		return nil, err
	}
	if isRegexOp && (right.WhichExpression() == fpb.Expression_Member_case || right.WhichExpression() == fpb.Expression_Value_case) {
		return nil, fmt.Errorf("regular expression match RHS must be a quoted string literal")
	}

	op, err := p.comparatorFromToken(comp)
	if err != nil {
		return nil, err
	}

	return &fpb.Expression{
		Expression: &fpb.Expression_Restriction{
			Restriction: &fpb.Restriction{
				Lhs:        left,
				Comparator: op,
				Rhs:        right,
			},
		},
	}, nil
}

func (p *parser) parseFunctionCall(funcName string) (*fpb.Expression, error) {
	p.advance() // consume '('
	var args []*fpb.Expression
	if p.peek().kind != tokRParen {
		for {
			arg, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.match(tokComma) {
				continue
			}
			break
		}
	}
	if !p.match(tokRParen) {
		return nil, errors.New("missing closing parenthesis in function call")
	}
	return &fpb.Expression{
		Expression: &fpb.Expression_Function{
			Function: &fpb.Function{
				Function: &fpb.Expression{
					Expression: &fpb.Expression_Value{Value: funcName},
				},
				Args: args,
			},
		},
	}, nil
}

func (p *parser) parseTerm() (*fpb.Expression, error) {
	tok := p.peek()
	if tok.kind == tokIdentifier {
		p.advance()
		// Check if it is a function call
		if p.peek().kind == tokLParen {
			return p.parseFunctionCall(tok.value)
		}

		if !strings.Contains(tok.value, ".") {
			return &fpb.Expression{
				Expression: &fpb.Expression_Value{Value: tok.value},
			}, nil
		}

		// Split dot-separated identifiers into individual member list elements
		parts := strings.Split(tok.value, ".")
		var list []*fpb.Expression
		for _, part := range parts {
			list = append(list, &fpb.Expression{
				Expression: &fpb.Expression_Value{Value: part},
			})
		}
		return &fpb.Expression{
			Expression: &fpb.Expression_Member{
				Member: &fpb.ExpressionList{Expressions: list},
			},
		}, nil
	}

	if tok.kind == tokString {
		p.advance()
		cleanVal := tok.value
		if (strings.HasPrefix(tok.value, "\"") && strings.HasSuffix(tok.value, "\"")) || (strings.HasPrefix(tok.value, "'") && strings.HasSuffix(tok.value, "'")) {
			cleanVal = tok.value[1 : len(tok.value)-1]
		}
		return &fpb.Expression{
			Expression: &fpb.Expression_Phrase{
				Phrase: &fpb.Phrase{
					Values: []string{cleanVal},
				},
			},
		}, nil
	}

	if tok.kind == tokInt || tok.kind == tokFloat || tok.kind == tokBool {
		p.advance()
		return &fpb.Expression{
			Expression: &fpb.Expression_Value{Value: tok.value},
		}, nil
	}

	return nil, fmt.Errorf("unexpected token %q at position %d", tok.value, tok.pos)
}
