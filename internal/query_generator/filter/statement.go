// Copyright 2026 Google LLC
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

package queryfilter

import (
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"
)

// Statement is the parameterized SQL statement that a Visitor or QueryGenerator appends SQL to.
// Its implementation defines how parameters are bound and which database Dialect renders
// backend-specific SQL predicates and clauses.
type Statement interface {
	// AppendSQL appends trusted SQL to the statement.
	AppendSQL(sql safesql.TrustedSQLString)
	// Bind records v as a query parameter and returns its placeholder.
	Bind(v any) safesql.TrustedSQLString
	// Dialect returns the database dialect used to render backend-specific SQL predicates.
	Dialect() queryfilterbase.Dialect
}

// noopDialect is used when Visitor.Q is nil (for example, during AST member scouting when
// rendered SQL is discarded).
type noopDialect struct{}

var _ queryfilterbase.Dialect = noopDialect{}

func (noopDialect) InArray(_, _ safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.EmptyString()
}

func (noopDialect) ArrayContains(_, _ safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.EmptyString()
}

func (noopDialect) RegexpMatch(_, _ safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.EmptyString()
}

func (noopDialect) TextSearch(_, _ safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.EmptyString()
}

func (noopDialect) OrderByClause(_ safesql.TrustedSQLString, _, _ bool) safesql.TrustedSQLString {
	return safesql.EmptyString()
}
