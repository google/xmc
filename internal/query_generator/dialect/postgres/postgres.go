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

// Package postgres provides the PostgreSQL statement builder and SQL dialect for query generation.
package postgres

import (
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"
)

// Statement is a parameterized PostgreSQL statement with positional arguments ($1, $2, ...).
type Statement struct {
	SQL  string
	Args []any
}

// NewStatement returns a Statement initialized with the given SQL string.
func NewStatement(sql string) *Statement {
	return &Statement{SQL: sql}
}

// AppendSQL appends trusted SQL to the statement.
func (s *Statement) AppendSQL(sql safesql.TrustedSQLString) {
	s.SQL = s.SQL + sql.String()
}

// Bind records v as a positional $N query parameter and returns its placeholder.
func (s *Statement) Bind(v any) safesql.TrustedSQLString {
	s.Args = append(s.Args, v)
	return safesql.NewBindingParam("$", uint64(len(s.Args)))
}

// Dialect returns the PostgreSQL SQL dialect.
func (s *Statement) Dialect() queryfilterbase.Dialect {
	return Dialect{}
}

// Dialect renders PostgreSQL SQL filter predicates.
// It expects array members to be referenced as plain array columns and full-text search columns to
// be TSVECTOR columns built with the 'simple' text search configuration.
type Dialect struct{}

var _ queryfilterbase.Dialect = Dialect{}

// InArray implements queryfilterbase.Dialect.
func (Dialect) InArray(col, arr safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), col, safesql.New(" = ANY("), arr, safesql.New("))"))
}

// ArrayContains implements queryfilterbase.Dialect.
func (Dialect) ArrayContains(arr, elem safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), elem, safesql.New(" = ANY("), arr, safesql.New("))"))
}

// RegexpMatch implements queryfilterbase.Dialect.
func (Dialect) RegexpMatch(lhs, rhs safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), lhs, safesql.New(" ~ "), rhs, safesql.New(")"))
}

// TextSearch implements queryfilterbase.Dialect.
func (Dialect) TextSearch(col, queryParam safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(col, safesql.New(" @@ plainto_tsquery('simple', "), queryParam, safesql.New(")"))
}
