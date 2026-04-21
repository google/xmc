package normalizer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenType int

const (
	tokenError tokenType = iota
	tokenEOF
	tokenIdent
	tokenString
	tokenNumber
	tokenOp
	tokenLParen
	tokenRParen
	tokenKeyword
)

// Token represents a single token in the input string.
type Token struct {
	Type    tokenType
	Literal string
	Raw     string
}

func (t Token) String() string {
	return fmt.Sprintf("Token(%v, %q)", t.Type, t.Literal)
}

// Tokenize scans the input string and returns a slice of Tokens.
func Tokenize(input string) []Token {
	var tokens []Token
	l := &lexer{input: input}

	for {
		tok := l.nextToken()
		if tok.Type == tokenEOF {
			break
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

type lexer struct {
	input string
	pos   int
	width int
}

var keywords = map[string]bool{
	"AND":   true,
	"OR":    true,
	"NOT":   true,
	"true":  true,
	"false": true,
	"null":  true,
}

func (l *lexer) nextToken() Token {
	l.skipWhitespace()

	if l.pos >= len(l.input) {
		return Token{Type: tokenEOF}
	}

	r, width := utf8.DecodeRuneInString(l.input[l.pos:])
	start := l.pos

	switch r {
	case '(', ')':
		l.pos += width
		tType := tokenLParen
		if r == ')' {
			tType = tokenRParen
		}
		return Token{Type: tType, Literal: string(r), Raw: string(r)}
	case ',':
		l.pos += width
		return Token{Type: tokenOp, Literal: ",", Raw: ","}
	case '=', ':', '!', '<', '>':
		l.pos += width
		nextR, nextW := utf8.DecodeRuneInString(l.input[l.pos:])
		if nextR == '=' {
			l.pos += nextW
		}
		raw := l.input[start:l.pos]
		return Token{Type: tokenOp, Literal: raw, Raw: raw}
	case '"':
		return l.scanString()
	case '-':
		nextR, _ := utf8.DecodeRuneInString(l.input[l.pos+width:])
		if unicode.IsDigit(nextR) {
			l.pos += width
			return l.scanLiteral(start, true)
		}

		l.pos += width
		return Token{Type: tokenOp, Literal: "-", Raw: "-"}
	}

	if !isDelimiter(r) {
		return l.scanLiteral(start, false)
	}

	l.pos += width
	return Token{Type: tokenError, Literal: string(r), Raw: string(r)}
}

func (l *lexer) scanString() Token {
	start := l.pos
	l.pos++

	var sb strings.Builder
	escaped := false

	for l.pos < len(l.input) {
		r, width := utf8.DecodeRuneInString(l.input[l.pos:])
		if escaped {
			sb.WriteRune(r)
			escaped = false
			l.pos += width
			continue
		}

		if r == '\\' {
			escaped = true
			l.pos += width
			continue
		}

		if r == '"' {
			l.pos += width
			break
		}

		sb.WriteRune(r)
		l.pos += width
	}

	raw := l.input[start:l.pos]
	return Token{Type: tokenString, Literal: sb.String(), Raw: raw}
}

func (l *lexer) scanLiteral(startPos int, hasNegativePrefix bool) Token {
	firstR, _ := utf8.DecodeRuneInString(l.input[startPos:])
	startsWithDigit := unicode.IsDigit(firstR) || hasNegativePrefix

	for l.pos < len(l.input) {
		r, width := utf8.DecodeRuneInString(l.input[l.pos:])

		if r == ':' {
			if startsWithDigit {
				nextR, _ := utf8.DecodeRuneInString(l.input[l.pos+width:])
				if unicode.IsDigit(nextR) {
					l.pos += width
					continue
				}
			}
			break
		}

		if isDelimiter(r) {
			break
		}
		l.pos += width
	}

	raw := l.input[startPos:l.pos]

	if isNumeric(raw) {
		return Token{Type: tokenNumber, Literal: raw, Raw: raw}
	}

	tType := tokenIdent
	if keywords[raw] {
		tType = tokenKeyword
	}

	return Token{Type: tType, Literal: raw, Raw: raw}
}

func isDelimiter(r rune) bool {
	return unicode.IsSpace(r) ||
		r == '(' || r == ')' ||
		r == '=' || r == ':' || r == '!' || r == '<' || r == '>' ||
		r == '"' || r == ','
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func (l *lexer) skipWhitespace() {
	for l.pos < len(l.input) {
		r, width := utf8.DecodeRuneInString(l.input[l.pos:])
		if !unicode.IsSpace(r) {
			break
		}
		l.pos += width
	}
}
