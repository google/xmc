// Package normalizer provides functions for normalizing user-provided
// query strings to a standardized format.
package normalizer

import (
	"fmt"
	"strings"

	"api/filter/go/filtervisitor"
	"xmanager_cloud/query_generator/querytools"
)

// Normalize takes a semi-compliant AIP-160 string and enforces quoting rules
// based on the provided schema.
func Normalize(input string, schema querytools.TableView) (string, error) {
	tokens := Tokenize(input)
	var sb strings.Builder

	var lastToken Token
	var insideFieldExpr bool // Are we immediately after "field ="
	var activeType filtervisitor.PrimType

	for i := 0; i < len(tokens); i++ {
		token := tokens[i]

		// If we see "and", "or", "not" (in any casing), we treat them as Keywords
		// so they are not quoted as search terms.
		if token.Type == tokenIdent {
			switch strings.ToUpper(token.Literal) {
			case "AND", "OR", "NOT":
				token.Raw = strings.ToUpper(token.Raw)
				token.Type = tokenKeyword
			}
		}

		// Add space between tokens unless tighter spacing is required.
		if i > 0 && shouldAddSpace(lastToken, token) {
			sb.WriteString(" ")
		}

		switch token.Type {
		case tokenOp:
			if token.Literal == "-" {
				// Check for the pattern "- <identifier>:" which indicates a NOT operation on an array field.
				if i+2 < len(tokens) && tokens[i+1].Type == tokenIdent && tokens[i+2].Type == tokenOp && tokens[i+2].Literal == ":" {
					fieldName := tokens[i+1].Literal
					fieldType := findInTable(schema, fieldName)
					if fieldType.Name == filtervisitor.Array {
						sb.WriteString("NOT")
						token.Type = tokenKeyword
						insideFieldExpr = false
						break
					}
				}
				// Unary negation, write token and don't set insideFieldExpr
				sb.WriteString(token.Raw)
				insideFieldExpr = false
				break
			}

			insideFieldExpr = true
			// The previous token is a field identifier, use it to determine the expected value type.
			if lastToken.Type == tokenIdent || lastToken.Type == tokenString {
				activeType = findInTable(schema, lastToken.Literal)
				if token.Literal == ":" && activeType.Name != filtervisitor.Array &&
					!(i+1 < len(tokens) && tokens[i+1].Type == tokenLParen) {
					sb.WriteString("=")
					break
				}
			}
			sb.WriteString(token.Raw)

		case tokenKeyword, tokenLParen, tokenRParen:
			insideFieldExpr = false
			sb.WriteString(token.Raw)

		case tokenIdent, tokenString, tokenNumber:
			if insideFieldExpr {
				// Token is a value in a field expression, fix its quoting based on type.
				val := fixValue(token, activeType)
				sb.WriteString(val)
				insideFieldExpr = false
			} else {
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
			}
		default:
			sb.WriteString(token.Raw)
		}

		lastToken = token
	}

	return sb.String(), nil
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
