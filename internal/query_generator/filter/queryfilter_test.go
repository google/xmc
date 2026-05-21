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
package queryfilter

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"api/filter/go/filter"
	"api/filter/go/filtervisitor"
	"api/filter/go/queryfilterbase"
	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"task/go/status"

	fpb "api/filter/filter_expression_go_proto"
)

var (
	textSearchColumns = []safesql.TrustedSQLString{safesql.New("TokenizedText"), safesql.New("TokenizedText2")}
)

type appendix struct {
	ql          safesql.TrustedSQLString
	tp          filtervisitor.PrimType
	optimizable bool
}

type successTest struct {
	input    string
	m        map[string]appendix
	output   []string
	bindings map[string]any
}

func memberMap(query *spanner.Statement, m map[string]appendix) MemberFunc {
	return func(name []string) (filtervisitor.PrimType, error) {
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return filtervisitor.Invalid, fmt.Errorf("name %q not found", n)
		}
		query.SQL = query.SQL + a.ql.String()
		return a.tp, nil
	}
}

func functionMap(m map[string]appendix) FunctionFunc {
	return func(name string, s *Spanner, el []*fpb.Expression) (filtervisitor.PrimType, error) {
		a, ok := m[name]
		if !ok {
			return filtervisitor.Invalid, fmt.Errorf("function %q not found", name)
		}
		s.Q.SQL = s.Q.SQL + a.ql.String() + "("
		if len(el) > 0 {
			if _, err := filtervisitor.Visit(s, el[0]); err != nil {
				return filtervisitor.Invalid, err
			}
			for _, e := range el[1:] {
				s.Q.SQL = s.Q.SQL + ", "
				if _, err := filtervisitor.Visit(s, e); err != nil {
					return filtervisitor.Invalid, err
				}
			}
		}
		s.Q.SQL = s.Q.SQL + ")"
		return a.tp, nil
	}
}

func disjunctionMap(query *spanner.Statement, m map[string]appendix) DisjunctionFunc {
	return func(name []string) (bool, safesql.TrustedSQLString, error) {
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return false, safesql.New(""), fmt.Errorf("name %q not found", n)
		}
		return a.optimizable, a.ql, nil
	}
}

func successDriver(optimized bool, t *testing.T, tests []successTest) {
	for _, tc := range tests {
		options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
		exp, err := filter.ParseExpressionWithOptions(options, tc.input)
		if err != nil {
			t.Errorf("ParseExpressionWithOptions({%+v}, %q): %v", options, tc.input, err)
			continue
		}
		query := spanner.NewStatement("SELECT x.foo FROM Blah AS x WHERE ")

		beforeLength := len(query.SQL)
		f := Spanner{
			MemberFunc:          memberMap(&query, tc.m),
			Q:                   &query,
			OptimizeDisjunction: optimized,
			DisjunctionFunc:     disjunctionMap(&query, tc.m),
		}
		f.FunctionFunc = functionMap(tc.m)
		if err := f.AppendFilters(exp); err != nil {
			t.Errorf("Append(%v): %v", exp, err)
		}

		success := false
		for _, out := range tc.output {
			if query.SQL[beforeLength:] == out {
				success = true
				break
			}
		}
		if !success {
			t.Errorf("got: %v, want one of the suffixes in %v", query.SQL, tc.output)
		}

		for name, want := range tc.bindings {
			got, ok := query.Params[name]
			if !ok {
				t.Errorf("missing binding %v", name)
				continue
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("binding diff (-want +got)\n:%s", diff)
			}
		}
	}
}

type failureTest struct {
	input string
	m     map[string]appendix
}

func failureDriver(t *testing.T, tests []failureTest) {
	for _, tc := range tests {
		options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
		exp, err := filter.ParseExpressionWithOptions(options, tc.input)
		if err != nil {
			t.Errorf("ParseExpressionWithOptions({%v}, %q): %v", options, tc.input, err)
			continue
		}
		query := spanner.NewStatement("SELECT x.foo FROM Blah AS x WHERE ")
		f := Spanner{MemberFunc: memberMap(&query, tc.m), Q: &query}
		f.FunctionFunc = functionMap(tc.m)
		if err := f.AppendFilters(exp); err == nil {
			t.Errorf("Append(%v); got %v, wanted error", exp, query.SQL)
		}
	}
}

func TestSingleLevelAppendSuccess(t *testing.T) {
	tests := []successTest{
		{
			input: `name = "42"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(x.name = @fb0)`},
		},
		{
			input: `count > 12`,
			m: map[string]appendix{
				"count": appendix{safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			output: []string{`(y.countz > @fb0)`},
		},
		{
			input: `count > 16183100702751628930`,
			m: map[string]appendix{
				"count": appendix{safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			output: []string{`(y.countz > @fb0)`},
		},
		{
			input: `humidity < 3.14`,
			m: map[string]appendix{
				"humidity": appendix{safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			output: []string{`(x.humidity < @fb0)`},
		},
		{
			input: `humidity >= 3`,
			m: map[string]appendix{
				"humidity": appendix{safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			output: []string{`(x.humidity >= @fb0)`},
		},
		{
			input: `state != "FIRST"`,
			m: map[string]appendix{
				"state": appendix{safesql.New("x.state"), filtervisitor.Integer, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `state != "FIRST"`,
			m: map[string]appendix{
				"state": appendix{safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `state = FIRST`,
			m: map[string]appendix{
				"state": appendix{safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state = @fb0)`},
		},
		{
			input: `state != FIRST`,
			m: map[string]appendix{
				"state": appendix{safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `bit`,
			m: map[string]appendix{
				"bit": appendix{safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			output: []string{`x.bit`},
		},
		{
			input: `bit = false`,
			m: map[string]appendix{
				"bit": appendix{safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			output: []string{`(x.bit = @fb0)`},
		},
		{
			input: `severity > 10`,
			m: map[string]appendix{
				"severity": appendix{safesql.New("x.severity"), filtervisitor.Unsigned, false},
			},
			output: []string{`(x.severity > @fb0)`},
		},
		{
			input: `time > "2012-11-01T22:08:41+00:00"`,
			m: map[string]appendix{
				"time": appendix{safesql.New("x.time"), filtervisitor.Timestamp, false},
			},
			output: []string{`(x.time > @fb0)`},
		},
		{
			input: `time > "2012-11-01T22:08:41+00:00"`,
			m: map[string]appendix{
				"time": appendix{safesql.New("x.proto.time.seconds"), filtervisitor.ProtoTimestamp, false},
			},
			output: []string{`(x.proto.time.seconds > @fb0)`},
		},
		{
			input: `date > "2012-11-01"`,
			m: map[string]appendix{
				"date": appendix{safesql.New("x.date"), filtervisitor.Date, false},
			},
			output: []string{`(x.date > @fb0)`},
		},
		{
			// Make sure the Date type parsing doesn't interfere
			// with string fields that happen to be date-shaped.
			input: `dateStr > "2012-11-01"`,
			m: map[string]appendix{
				"dateStr": appendix{safesql.New("x.dateStr"), filtervisitor.String, false},
			},
			output: []string{`(x.dateStr > @fb0)`},
		},
		{
			input: `date != "2012-11-01"`,
			m: map[string]appendix{
				"date": appendix{safesql.New("x.date"), filtervisitor.Date, false},
			},
			output: []string{`(x.date != @fb0)`},
		},
		{
			input: `name =~ "^match$"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(REGEXP_CONTAINS(x.name , @fb0))`},
		},
		{
			input: `name !~ "^match$"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(NOT (REGEXP_CONTAINS(x.name , @fb0)))`},
		},
		{
			input: `foo(count) !~ "^match$"`,
			m: map[string]appendix{
				"count": appendix{safesql.New("x.count"), filtervisitor.Integer, false},
				"foo":   appendix{safesql.New("bar"), filtervisitor.String, false},
			},
			output: []string{`(NOT (REGEXP_CONTAINS(bar(x.count) , @fb0)))`},
		},
		{
			input: `color: "blue"`,
			m: map[string]appendix{
				"color": appendix{safesql.New("x.color"), filtervisitor.ArrayType(&filtervisitor.StrConst), false},
			},
			output: []string{`(@fb0 IN x.color)`},
		},
		{
			input: `foo(name)`,
			m: map[string]appendix{
				"foo":  appendix{safesql.New("bar"), filtervisitor.Boolean, false},
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name)`},
		},
		{
			input: `foo(name, "asdf")`,
			m: map[string]appendix{
				"foo":  appendix{safesql.New("bar"), filtervisitor.Boolean, false},
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name, @fb0)`},
		},
		{
			input: `foo(name, bar("asdf"))`,
			m: map[string]appendix{
				"foo":  appendix{safesql.New("bar"), filtervisitor.Boolean, false},
				"bar":  appendix{safesql.New("baz"), filtervisitor.Boolean, false},
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name, baz(@fb0))`},
		},
		{
			input: `bytesfield = "bbb"`,
			m: map[string]appendix{
				"bytesfield": appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			output: []string{`(x.bytesfield = @fb0)`},
			bindings: map[string]any{
				"fb0": []byte("bbb"),
			},
		},
		{
			input: `bytesfield > "bbb"`,
			m: map[string]appendix{
				"bytesfield": appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			output: []string{`(x.bytesfield > @fb0)`},
			bindings: map[string]any{
				"fb0": []byte("bbb"),
			},
		},
		{
			input: `bytesfield =~ "^match$"`,
			m: map[string]appendix{
				"bytesfield": appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			output: []string{`(REGEXP_CONTAINS(x.bytesfield , @fb0))`},
			bindings: map[string]any{
				"fb0": []byte("^match$"),
			},
		},
	}

	successDriver(false, t, tests)
}

func TestSingleLevelAppendFailure(t *testing.T) {
	tests := []failureTest{
		{
			`nmae = "42"`,
			map[string]appendix{},
		},
		{
			`count > 12 AND stat = "12"`,
			map[string]appendix{
				"count": appendix{safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`len(name) > 12`,
			map[string]appendix{},
		},
		{
			`name: "1"`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`bti`,
			map[string]appendix{},
		},
		{
			`foo(name)`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},

		// Type failures.
		{
			`name >= 42`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`state = 123.32`,
			map[string]appendix{
				"state": appendix{safesql.New("x.state"), filtervisitor.Enum, false},
			},
		},
		{
			`count <= "12"`,
			map[string]appendix{
				"count": appendix{safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`name AND count >= 42`,
			map[string]appendix{
				"name":  appendix{safesql.New("x.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`name = "blah" AND count`,
			map[string]appendix{
				"name":  appendix{safesql.New("x.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`severity > -10`,
			map[string]appendix{
				"severity": appendix{safesql.New("x.severity"), filtervisitor.Unsigned, false},
			},
		},
		{
			`NOT name`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`humidity`,
			map[string]appendix{
				"humidity": appendix{safesql.New("x.humidity"), filtervisitor.Float, false},
			},
		},
		{
			`color: "blue"`,
			map[string]appendix{
				"color": appendix{safesql.New("x.color"), filtervisitor.ArrayType(&queryfilterbase.IntConst), false},
			},
		},
		{
			`date > "not a date"`,
			map[string]appendix{
				"date": appendix{safesql.New("x.date"), filtervisitor.Date, false},
			},
		},
		{
			`notastring =~ "^match$"`,
			map[string]appendix{
				"notastring": appendix{safesql.New("x.notastring"), filtervisitor.Integer, false},
			},
		},
		{
			`notastring !~ "^match$"`,
			map[string]appendix{
				"notastring": appendix{safesql.New("x.notastring"), filtervisitor.Integer, false},
			},
		},
		{
			`bytesfield = stringfield`,
			map[string]appendix{
				"bytesfield":  appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
				"stringfield": appendix{safesql.New("x.stringfield"), filtervisitor.String, false},
			},
		},
		{
			`bytesfield = 12.0`,
			map[string]appendix{
				"bytesfield": appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
		},
		{
			`bytesfield = constval`,
			map[string]appendix{
				"bytesfield": appendix{safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
				"constval":   appendix{safesql.New("myconst"), filtervisitor.StrConst, false},
			},
		},
		// The below regex syntax is supported by the parser,
		// but currently we only allow the RHS to be a string literal.
		{
			`name =~ ("foo" AND ("bar" OR "baz"))`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`name =~ (NOT "^re")`,
			map[string]appendix{
				"name": appendix{safesql.New("x.name"), filtervisitor.String, false},
			},
		},
	}

	failureDriver(t, tests)
}

func TestMultiLevelAppendSuccess(t *testing.T) {
	tests := []successTest{
		{
			input: `str = "42"`,
			m: map[string]appendix{
				"str": appendix{safesql.New("y.str"), filtervisitor.String, false},
			},
			output: []string{`(y.str = @fb0)`},
		},
		{
			input: `msg.name = "hello"`,
			m: map[string]appendix{
				"msg.name": appendix{safesql.New("y.msg.name"), filtervisitor.String, false},
			},
			output: []string{`(y.msg.name = @fb0)`},
		},
		{
			input: `recurse.msg.name = "bye"`,
			m: map[string]appendix{
				"recurse.msg.name": appendix{safesql.New("y.recurse.msg.name"), filtervisitor.String, false},
			},
			output: []string{`(y.recurse.msg.name = @fb0)`},
		},
	}

	successDriver(false, t, tests)
}

func TestComplexSuccess(t *testing.T) {
	tests := []successTest{
		{
			input: `name = "42" count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) AND (z.count > @fb1))`},
		},
		{
			input: `"23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((@fb0 = z.name) AND (z.count > @fb1))`},
		},
		{
			input: `name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1))`},
		},
		{
			input: `name = "42" OR count != 42 OR -state=FIRST`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
				"state": appendix{safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1) OR NOT (z.state = @fb2))`},
		},
		{
			input: `name = "42" AND count = 1 OR count = 2 OR count = 3`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) AND ((z.count = @fb1) OR (z.count = @fb2) OR (z.count = @fb3)))`},
		},
		{
			input: `-name="42"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`NOT (z.name = @fb0)`},
		},
		{
			input: `a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": appendix{safesql.New("z.a"), filtervisitor.Boolean, false},
				"b": appendix{safesql.New("z.b"), filtervisitor.Boolean, false},
				"c": appendix{safesql.New("z.c"), filtervisitor.Boolean, false},
				"d": appendix{safesql.New("z.d"), filtervisitor.Boolean, false},
				"e": appendix{safesql.New("z.e"), filtervisitor.Boolean, false},
			},
			output: []string{`(z.a AND (z.b OR z.c) AND (z.d OR z.e))`},
		},
		{
			input: `(count > 1 AND name !~ "^re") OR name =~ "re$" OR state=FIRST`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
				"state": appendix{safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`(((z.count > @fb0) AND (NOT (REGEXP_CONTAINS(z.name , @fb1)))) OR (REGEXP_CONTAINS(z.name , @fb2)) OR (z.state = @fb3))`},
		},
		{
			input: `(count > 1 AND name !~ "^re") OR name =~ "re$" OR state=FIRST`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
				"state": appendix{safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`(((z.count > @fb0) AND (NOT (REGEXP_CONTAINS(z.name , @fb1)))) OR (REGEXP_CONTAINS(z.name , @fb2)) OR (z.state = @fb3))`},
		},
	}

	successDriver(false, t, tests)
}

func TestOptimizedSuccess(t *testing.T) {
	tests := []successTest{
		{
			input: `name = "42" count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, true},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) AND (z.count > @fb1))`},
		},
		{
			input: `"23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, true},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((@fb0 = z.name) AND (z.count > @fb1))`},
		},
		{
			input: `name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, true},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1))`, `((z.count != @fb0) OR (z.name = @fb1))`},
		},
		{
			input: `name = "42" AND nodes.count = "1" OR nodes.count = "2" OR nodes.count = "3"`,
			m: map[string]appendix{
				"name":        appendix{safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": appendix{safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.name = @fb0) AND ((z.count IN UNNEST (@fb1))))`},
		},
		{
			input: `name = "42" OR nodes.count = "1"`,
			m: map[string]appendix{
				"name":        appendix{safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": appendix{safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count = @fb1))`, `((z.count = @fb0) OR (z.name = @fb1))`},
		},
		{
			input: `name = "42" OR name = "47" OR nodes.count = "1"`,
			m: map[string]appendix{
				"name":        appendix{safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": appendix{safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.count = @fb0) OR (z.name IN UNNEST (@fb1)))`, `((z.name IN UNNEST (@fb0)) OR (z.count = @fb1))`},
		},
		{
			input: `name = "42" OR nodes.count = 1 OR nodes.count = 2 OR nodes.count = 3`,
			m: map[string]appendix{
				"name":        appendix{safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": appendix{safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count IN UNNEST (@fb1)))`, `((z.count IN UNNEST (@fb0)) OR (z.name = @fb1))`},
		},
		{
			input: `nodes.name = "42" OR nodes.name = "44" OR nodes.count = 1 OR nodes.count = 2 OR nodes.count = 3 OR nodes.name = "45"`,
			m: map[string]appendix{
				"nodes.name":  appendix{safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": appendix{safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name IN UNNEST (@fb0)) OR (z.count IN UNNEST (@fb1)))`, `((z.count IN UNNEST (@fb0)) OR (z.name IN UNNEST (@fb1)))`},
		},
		{
			input: `-name="42"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("z.name"), filtervisitor.String, true},
			},
			output: []string{`NOT (z.name = @fb0)`},
		},
		{
			input: `a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": appendix{safesql.New("z.a"), filtervisitor.Boolean, true},
				"b": appendix{safesql.New("z.b"), filtervisitor.Boolean, true},
				"c": appendix{safesql.New("z.c"), filtervisitor.Boolean, true},
				"d": appendix{safesql.New("z.d"), filtervisitor.Boolean, true},
				"e": appendix{safesql.New("z.e"), filtervisitor.Boolean, true},
			},
			output: []string{`(z.a AND (z.b OR z.c) AND (z.d OR z.e))`},
		},
	}

	successDriver(true, t, tests)
}

func TestRestrictionTranslate(t *testing.T) {

	type TestCase struct {
		description    string
		query          safesql.TrustedSQLString
		filter         string
		member         map[string]appendix
		sub            map[string]string
		outputSQL      string
		outputBindings map[string]any
	}

	tests := []TestCase{
		{
			"Basic Substitution",
			safesql.New("SELECT x.f FROM b AS x WHERE"),
			"kind = \"FOO_OLD\"",
			map[string]appendix{
				"kind": appendix{safesql.New("z.kind"), filtervisitor.String, true},
			},
			map[string]string{"FOO_OLD": "FOO_NEW"},
			"SELECT x.f FROM b AS x WHERE(z.kind = @fb0)",
			map[string]any{"fb0": "FOO_NEW"},
		},
		{
			"Basic Substitution - HAS operator",
			safesql.New("SELECT x.f FROM b AS x WHERE"),
			"kind : \"FOO_OLD\"",
			map[string]appendix{
				"kind": appendix{safesql.New("z.kind"), filtervisitor.ArrayType(&filtervisitor.String), true},
			},
			map[string]string{"FOO_OLD": "FOO_NEW"},
			"SELECT x.f FROM b AS x WHERE(@fb0 IN z.kind)",
			map[string]any{"fb0": "FOO_NEW"},
		},
		{
			"Inverted equality",
			safesql.New("SELECT x.f FROM b AS x WHERE"),
			"\"FOO_OLD\" = kind",
			map[string]appendix{
				"kind": appendix{safesql.New("z.kind"), filtervisitor.String, true},
			},
			map[string]string{"FOO_OLD": "FOO_NEW"},
			"SELECT x.f FROM b AS x WHERE(@fb0 = z.kind)",
			map[string]any{"fb0": "FOO_NEW"},
		},
		{
			"No match - no rewrite",
			safesql.New("SELECT x.f FROM b AS x WHERE"),
			"\"FOO_OLD\" = other_name",
			map[string]appendix{
				"kind":       appendix{safesql.New("z.kind"), filtervisitor.String, true},
				"other_name": appendix{safesql.New("z.other"), filtervisitor.String, true},
			},
			map[string]string{"FOO_OLD": "FOO_NEW"},
			"SELECT x.f FROM b AS x WHERE(@fb0 = z.other)",
			map[string]any{"fb0": "FOO_OLD"},
		},
	}

	for _, tc := range tests {
		exp, err := filter.ParseExpression(tc.filter)
		if err != nil {
			t.Errorf("%s - ParseExpression(%q): %v", tc.description, tc.filter, err)
			continue
		}
		query := spanner.NewStatement(tc.query.String())

		f := Spanner{MemberFunc: memberMap(&query, tc.member), Q: &query}
		f.FunctionFunc = functionMap(tc.member)
		f.RestrictionTranslate = func(r *fpb.Restriction) *fpb.Restriction {
			field := ""
			phrase := ""
			if r.GetLhs().GetValue() != "" && r.GetRhs().GetPhrase() != nil {
				field = r.GetLhs().GetValue()
				phrase = r.GetRhs().GetPhrase().GetValues()[0]
			} else if r.GetRhs().GetValue() != "" && r.GetLhs().GetPhrase() != nil {
				field = r.GetRhs().GetValue()
				phrase = r.GetLhs().GetPhrase().GetValues()[0]
			} else {
				return r
			}
			if field != "kind" {
				return r
			}

			newphrase := tc.sub[phrase]
			out := &fpb.Restriction{}
			proto.Merge(out, r)
			if r.GetLhs().GetPhrase() != nil {
				out.GetLhs().GetPhrase().GetValues()[0] = newphrase
			} else {
				out.GetRhs().GetPhrase().GetValues()[0] = newphrase
			}
			return out

		}
		if err := f.AppendFilters(exp); err != nil {
			t.Errorf("%s - Append(%v): %v", tc.description, exp, err)
			continue
		}

		if tc.outputSQL != query.SQL {
			t.Errorf("%s - want: %q, got %q", tc.description, tc.outputSQL, query.SQL)
		}

		if len(tc.outputBindings) != len(query.Params) {
			t.Errorf("%s - want: %q, got %q", tc.description, tc.outputBindings, query.Params)
		}

		for k, v := range tc.outputBindings {
			if query.Params[k] != v {
				t.Errorf("%s - want: %q, got %q", tc.description, tc.outputBindings[k], query.Params[k])
			}
		}
	}
}

func TestRegexpLiteral(t *testing.T) {
	tests := []struct {
		desc    string
		input   string
		want    string
		wantErr bool
	}{
		{
			desc:  "extracts_string_literal_when_MATCHES_REGEXP",
			input: `x =~ "^match"`,
			want:  "^match",
		},
		{
			desc:  "extracts_string_literal_when_NOT_MATCHES_REGEXP",
			input: `x !~ "^match"`,
			want:  "^match",
		},
		{
			desc:    "fails_when_rhs_unary_expression",
			input:   `x =~ (NOT "a")`,
			wantErr: true,
		},
		{
			desc:    "fails_when_rhs_binary_expression",
			input:   `x =~ ("a" OR "b")`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			expr, err := filter.ParseExpressionWithOptions(options, tc.input)

			if err != nil {
				t.Fatalf("ParseExpressionWithOptions({%v}, %q) got err %v, want success", options, tc.input, err)
			}

			got, err := queryfilterbase.RegexpLiteral(expr.GetRestriction())
			if err != nil && !tc.wantErr {
				t.Errorf("regexpLiteral({%v}) got err %v, want success", expr, err)
			}
			if err == nil && tc.wantErr {
				t.Errorf("regexpLiteral({%v}) got %q, want error", expr, got)
			}

			if tc.wantErr {
				return // further diff not applicable if we wanted failure
			}

			if got != tc.want {
				t.Errorf("regexpLiteral({%v}) got %q, want %q", expr, got, tc.want)
			}
		})
	}
}

// TestRegexpLiteral_ExpectedParserErrors does not test queryfilter itself,
// but rather codifies parser behavior that `regexLiteral` relies on.
func TestRegexpLiteral_ExpectedParserErrors(t *testing.T) {
	tests := []struct {
		desc  string
		input string
	}{
		{
			desc:  "rhs_token",
			input: `x =~ NOT`,
		},
		{
			desc:  "rhs_unquoted_string",
			input: `x =~ abc`,
		},
		{
			desc:  "rhs_not_string",
			input: `x =~ 123`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			expr, err := filter.ParseExpressionWithOptions(options, tc.input)
			if err == nil {
				t.Errorf("ParseExpressionWithOptions({%v}, %q) got {%v}, want error", options, tc.input, expr)
			}
		})
	}
}

// TestRestrictionWithFullTextSearch tests the full text once with FTS enabled and another without.
func TestRestrictionWithFullTextSearch(t *testing.T) {
	tests := []struct {
		input    string
		m        map[string]appendix
		output   []string
		bindings map[string]any
	}{
		{
			input: `"full text search"`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`(SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1))`},
		},
		{
			input: `"full text search" AND name = "42" count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) AND (z.count > @fb3)))`},
		},
		{
			input: `"full text search" AND "23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND (@fb2 = z.name) AND (z.count > @fb3))`},
		},
		{
			input: `"full text search" AND name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) OR (z.count != @fb3)))`},
		},
		{
			input: `"full text search" AND name = "42" OR count != 42 OR -state=FIRST`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
				"state": appendix{safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) OR (z.count != @fb3) OR NOT (z.state = @fb4)))`},
		},
		{
			input: `"full text search" AND name = "42" AND count = 1 OR count = 2 OR count = 3`,
			m: map[string]appendix{
				"name":  appendix{safesql.New("z.name"), filtervisitor.String, false},
				"count": appendix{safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND (z.name = @fb2) AND ((z.count = @fb3) OR (z.count = @fb4) OR (z.count = @fb5)))`},
		},
		{
			input: `"full text search" AND -name="42"`,
			m: map[string]appendix{
				"name": appendix{safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND NOT (z.name = @fb2))`},
		},
		{
			input: `"full text search" AND a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": appendix{safesql.New("z.a"), filtervisitor.Boolean, false},
				"b": appendix{safesql.New("z.b"), filtervisitor.Boolean, false},
				"c": appendix{safesql.New("z.c"), filtervisitor.Boolean, false},
				"d": appendix{safesql.New("z.d"), filtervisitor.Boolean, false},
				"e": appendix{safesql.New("z.e"), filtervisitor.Boolean, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND z.a AND (z.b OR z.c) AND (z.d OR z.e))`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			for i := range 2 {
				options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
				exp, err := filter.ParseExpressionWithOptions(options, tc.input)
				if err != nil {
					t.Fatalf("ParseExpressionWithOptions({%v}, %q) got err %v, want success", options, tc.input, err)
				}

				query := spanner.NewStatement("SELECT x.foo FROM Blah AS x WHERE ")
				beforeLength := len(query.SQL)
				f := Spanner{
					MemberFunc:        memberMap(&query, tc.m),
					Q:                 &query,
					DisjunctionFunc:   disjunctionMap(&query, tc.m),
					TextSearchColumns: textSearchColumns,
				}

				ftsEnabled := i == 0
				if ftsEnabled {
					f.FilterBaseOptions = []FilterBaseOption{WithFullTextSearchEnabled}
				}

				err = f.AppendFilters(exp)
				if err != nil && ftsEnabled {
					success := false
					for _, out := range tc.output {
						if query.SQL[beforeLength:] == out {
							success = true
							break
						}
					}
					if !success {
						t.Errorf("got: %v, want one of the suffixes in %v", query.SQL, tc.output)
					}
				}

				if errors.Is(err, status.ErrInvalidArgument) {
					t.Errorf("got: %v, want success", err)
				}
			}
		})
	}
}

func TestHasOperatorWithSubstringMatching(t *testing.T) {
	tests := []struct {
		input    string
		m        map[string]appendix
		output   []string
		bindings map[string]any
	}{
		{
			input: `name : 42`,
			m: map[string]appendix{
				"name": appendix{safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`(STRPOS(z.name, @fb0) > 0)`},
		},
		{
			input: `name:vrt.43d81b476a52`,
			m: map[string]appendix{
				"name": appendix{safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`(STRPOS(z.name, @fb0) > 0)`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			exp, err := filter.ParseExpressionWithOptions(options, tc.input)
			if err != nil {
				t.Fatalf("ParseExpressionWithOptions({%v}, %q) got err %v, want success", options, tc.input, err)
			}

			query := spanner.NewStatement("SELECT x.foo FROM Blah AS x WHERE ")
			beforeLength := len(query.SQL)
			f := Spanner{
				MemberFunc:        memberMap(&query, tc.m),
				Q:                 &query,
				FilterBaseOptions: []FilterBaseOption{WithHasOperatorAsContains},
			}

			err = f.AppendFilters(exp)
			if err != nil {
				success := false
				for _, out := range tc.output {
					if query.SQL[beforeLength:] == out {
						success = true
						break
					}
				}
				if !success {
					t.Errorf("got: %v, want one of the suffixes in %v", query.SQL, tc.output)
				}
			}

			if errors.Is(err, status.ErrInvalidArgument) {
				t.Errorf("got: %v, want success", err)
			}
		})
	}
}
