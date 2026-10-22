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
package querytools

import (
	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/query_generator/filter"
	"xmanager_cloud/internal/safesql"
)

// Unnester applies unnests to a given query, re-aggregating the unnested fields and
// patching those re-aggregated fields back into a pristine proto field so that only
// the filtered rows are returned.
type Unnester interface {
	// Apply takes a query consisting of just a "SELECT ..." that may be augmented
	// with the re-aggregated unnests, and a "FROM ..." clause that may be augmented with
	// the "UNNEST ..." statements.
	Apply(q queryfilter.Statement, from []safesql.TrustedSQLString) error
	// Patch takes the proto we're unnesting / filtering and a result row, it then
	// delegates to each of the unnests to patch their re-aggregation column back into
	// the pristine proto column so that only the filtered results are returned.
	Patch(st any, row *spanner.Row) error
}

// patch represents a single unnests callback to patch its re-aggregation column
// (see aggregator in unnest) extracted from the result row into the pristine
// protobuf passed as the first argument.
type patch func(any, *spanner.Row) error

// Unnest describes the unnest for a specific field.
type Unnest struct {
	// The re-aggregation for the SELECT statement.
	Aggregator safesql.TrustedSQLString
	// The UNNEST clause to add to a FROM
	Clause safesql.TrustedSQLString
	// Any bindings from the other clauses that we must attach
	Bindings map[string]any
	// Additional statements for the where clause that may be required for
	// joining.
	Where safesql.TrustedSQLString
	// Function that patches the re-aggregated field into the result.
	Patch patch
}

type unnester struct {
	seen map[*Unnest]bool
}

// Apply implements Unnester
func (unr *unnester) Apply(q queryfilter.Statement, from []safesql.TrustedSQLString) error {
	// Add re-aggregation fields to the SELECT
	for unnest := range unr.seen {
		if unnest == nil || unnest.Aggregator == safesql.New("") {
			continue
		}
		q.AppendSQL(safesql.Concat(safesql.New(", "), unnest.Aggregator))
	}
	// Add the base FROM statement.
	for _, f := range from {
		q.AppendSQL(f)
	}
	// Add UNNESTs to FROM
	for unnest := range unr.seen {
		if unnest == nil {
			continue
		}
		if unnest.Clause != safesql.New("") {
			q.AppendSQL(safesql.Concat(safesql.New(", "), unnest.Clause))
		}
	}
	return nil
}

// Patch implements Unnester
func (unr *unnester) Patch(st any, row *spanner.Row) error {
	// For each unnested field, attempt to patch the re-aggregated form
	// into the protobuf so that only the matching entries are returned.
	for unnest := range unr.seen {
		if unnest == nil {
			continue
		}
		if err := unnest.Patch(st, row); err != nil {
			return err
		}
	}
	return nil
}
