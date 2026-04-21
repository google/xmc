package querytools

import (
	"strings"

	fpb "api/filter/filter_expression_go_proto"
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/query_generator/filter/queryfilter"
	"task/go/status"
)

// Unnester applies unnests to a given query, re-aggregating the unnested fields and
// patching those re-aggregated fields back into a pristine proto field so that only
// the filtered rows are returned.
type Unnester interface {
	// Apply takes a query consisting of just a "SELECT ..." that may be augmented
	// with the re-aggregated unnests, and a "FROM ..." clause that may be augmented with
	// the "UNNEST ..." statements.
	Apply(q *spanner.Statement, from []safesql.TrustedSQLString) error
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
func (unr *unnester) Apply(q *spanner.Statement, from []safesql.TrustedSQLString) error {
	// Add re-aggregation fields to the SELECT
	for unnest := range unr.seen {
		if unnest == nil || unnest.Aggregator == safesql.New("") {
			continue
		}
		q.SQL = q.SQL + ", " + unnest.Aggregator.String()
	}
	// Add the base FROM statement.
	for _, f := range from {
		q.SQL = q.SQL + f.String()
	}
	// Add UNNESTs to FROM
	for unnest := range unr.seen {
		if unnest == nil {
			continue
		}
		if unnest.Clause != safesql.New("") {
			q.SQL = q.SQL + ", " + unnest.Clause.String()
		}
		if q.Params == nil {
			q.Params = make(map[string]any)
		}
		for k, v := range unnest.Bindings {
			q.Params[k] = v
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

// NewUnnester returns a new object implementing Unnester based on the "unnest"s discovered by processing the provided filter expression
// with the provided member/function maps.
func NewUnnester(members map[string]Member, functions map[string]Function, filterPredicate *fpb.Expression) (Unnester, error) {
	unr := &unnester{
		seen: make(map[*Unnest]bool),
	}
	// We should do nothing in the case where there is no filter.
	if filterPredicate == nil {
		return unr, nil
	}
	// Do a preliminary walk over the filterPredicate expression, so that we can scout what members it accesses.
	// We use a "dummy query" here to recycle the logic in queryfilter.Spanner that walks the expression and calls
	// us back on members.
	dummyQuery := spanner.NewStatement("")
	qf := queryfilter.Spanner{
		MemberFunc: func(name []string) (filtervisitor.PrimType, error) {
			n := strings.Join(name, ".")
			m, ok := members[n]
			if !ok {
				return filtervisitor.Invalid, status.Errorf(status.InvalidArgument, "unsupported filter field in query: %q", n)
			}
			unr.seen[m.UnnestClause] = true
			return m.Type, nil
		},
		FunctionFunc: FunctionMap(functions),
		Q:            &dummyQuery,
	}
	err := qf.AppendFilters(filterPredicate)
	if err != nil {
		return nil, err
	}
	return unr, nil
}
