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

// Package spanner provides the Cloud Spanner statement builder and SQL dialect for query generation.
package spanner

import (
	"fmt"
	"sync/atomic"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"
)

// Statement appends SQL and named @fbN parameters to a Cloud Spanner statement.
type Statement struct {
	q     *spanner.Statement
	count uint64
}

// NewStatement returns a Statement that appends to the Cloud Spanner statement q.
func NewStatement(q *spanner.Statement) *Statement {
	return &Statement{q: q}
}

// AppendSQL appends trusted SQL to the underlying Cloud Spanner statement.
func (s *Statement) AppendSQL(sql safesql.TrustedSQLString) {
	s.q.SQL = s.q.SQL + sql.String()
}

// Bind records v as a named @fbN query parameter and returns its placeholder.
func (s *Statement) Bind(v any) safesql.TrustedSQLString {
	index := atomic.AddUint64(&s.count, 1) - 1
	if s.q.Params == nil {
		s.q.Params = make(map[string]any)
	}
	s.q.Params[fmt.Sprintf("fb%d", index)] = v
	return safesql.NewBindingParam("@fb", index)
}

// Dialect returns the Cloud Spanner SQL dialect.
func (s *Statement) Dialect() queryfilterbase.Dialect {
	return Dialect{}
}

// Dialect renders Cloud Spanner SQL filter predicates.
type Dialect struct{}

var _ queryfilterbase.Dialect = Dialect{}

// InArray implements queryfilterbase.Dialect.
func (Dialect) InArray(col, arr safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), col, safesql.New(" IN UNNEST ("), arr, safesql.New("))"))
}

// ArrayContains implements queryfilterbase.Dialect.
func (Dialect) ArrayContains(arr, elem safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("("), elem, safesql.New(" IN UNNEST("), arr, safesql.New("))"))
}

// RegexpMatch implements queryfilterbase.Dialect.
func (Dialect) RegexpMatch(lhs, rhs safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("(REGEXP_CONTAINS("), lhs, safesql.New(" , "), rhs, safesql.New("))"))
}

// TextSearch implements queryfilterbase.Dialect.
func (Dialect) TextSearch(col, queryParam safesql.TrustedSQLString) safesql.TrustedSQLString {
	return safesql.Concat(safesql.New("SEARCH("), col, safesql.New(", "), queryParam, safesql.New(")"))
}
