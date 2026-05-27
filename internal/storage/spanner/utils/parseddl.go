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
