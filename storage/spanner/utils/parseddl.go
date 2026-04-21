// Package spannerutils provides utility functions for interacting with Spanner.
package spannerutils

import (
	"regexp"
	"strings"
)

var commentRegex = regexp.MustCompile(`--.*`)

// ParseDDL parses a DDL string into a list of statements.
// It removes comments and splits the string by semicolons.
func ParseDDL(ddl string) []string {
	ddl = commentRegex.ReplaceAllString(ddl, "") // remove comments
	var statements []string
	for _, s := range strings.Split(ddl, ";") {
		s = strings.TrimSpace(s)
		if len(s) > 0 {
			statements = append(statements, s)
		}
	}
	return statements
}
