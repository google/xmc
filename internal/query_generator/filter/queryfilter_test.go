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
package queryfilter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/api/filter/go/queryfilterbase"
	"xmanager_cloud/internal/safesql"

	fpb "xmanager_cloud/api/filter"
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
	return func(name []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return safesql.New(""), filtervisitor.Invalid, fmt.Errorf("name %q not found", n)
		}
		return a.ql, a.tp, nil
	}
}

func functionMap(m map[string]appendix) FunctionFunc {
	return func(name string, s *Spanner, el []*fpb.Expression) (filtervisitor.PrimType, error) {
		a, ok := m[name]
		if !ok {
			return filtervisitor.Invalid, fmt.Errorf("function %q not found", name)
		}
		s.AppendTrustedSQL(safesql.Concat(a.ql, safesql.New("(")))
		if len(el) > 0 {
			if _, err := filtervisitor.Visit(s, el[0]); err != nil {
				return filtervisitor.Invalid, err
			}
			for _, e := range el[1:] {
				s.AppendTrustedSQL(safesql.New(", "))
				if _, err := filtervisitor.Visit(s, e); err != nil {
					return filtervisitor.Invalid, err
				}
			}
		}
		s.AppendTrustedSQL(safesql.New(")"))
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
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(x.name = @fb0)`},
		},
		{
			input: `count > 12`,
			m: map[string]appendix{
				"count": {safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			output: []string{`(y.countz > @fb0)`},
		},
		{
			input: `count > 16183100702751628930`,
			m: map[string]appendix{
				"count": {safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			output: []string{`(y.countz > @fb0)`},
		},
		{
			input: `humidity < 3.14`,
			m: map[string]appendix{
				"humidity": {safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			output: []string{`(x.humidity < @fb0)`},
		},
		{
			input: `humidity >= 3`,
			m: map[string]appendix{
				"humidity": {safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			output: []string{`(x.humidity >= @fb0)`},
		},
		{
			input: `state != "FIRST"`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Integer, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `state != "FIRST"`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `state = FIRST`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state = @fb0)`},
		},
		{
			input: `state != FIRST`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			output: []string{`(x.state != @fb0)`},
		},
		{
			input: `bit`,
			m: map[string]appendix{
				"bit": {safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			output: []string{`x.bit`},
		},
		{
			input: `bit = false`,
			m: map[string]appendix{
				"bit": {safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			output: []string{`(x.bit = @fb0)`},
		},
		{
			input: `severity > 10`,
			m: map[string]appendix{
				"severity": {safesql.New("x.severity"), filtervisitor.Unsigned, false},
			},
			output: []string{`(x.severity > @fb0)`},
		},
		{
			input: `time > "2012-11-01T22:08:41+00:00"`,
			m: map[string]appendix{
				"time": {safesql.New("x.time"), filtervisitor.Timestamp, false},
			},
			output: []string{`(x.time > @fb0)`},
		},
		{
			input: `time > "2012-11-01T22:08:41+00:00"`,
			m: map[string]appendix{
				"time": {safesql.New("x.proto.time.seconds"), filtervisitor.ProtoTimestamp, false},
			},
			output: []string{`(x.proto.time.seconds > @fb0)`},
		},
		{
			input: `date > "2012-11-01"`,
			m: map[string]appendix{
				"date": {safesql.New("x.date"), filtervisitor.Date, false},
			},
			output: []string{`(x.date > @fb0)`},
		},
		{
			// Make sure the Date type parsing doesn't interfere
			// with string fields that happen to be date-shaped.
			input: `dateStr > "2012-11-01"`,
			m: map[string]appendix{
				"dateStr": {safesql.New("x.dateStr"), filtervisitor.String, false},
			},
			output: []string{`(x.dateStr > @fb0)`},
		},
		{
			input: `date != "2012-11-01"`,
			m: map[string]appendix{
				"date": {safesql.New("x.date"), filtervisitor.Date, false},
			},
			output: []string{`(x.date != @fb0)`},
		},
		{
			input: `name =~ "^match$"`,
			m: map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(REGEXP_CONTAINS(x.name , @fb0))`},
		},
		{
			input: `name !~ "^match$"`,
			m: map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`(NOT (REGEXP_CONTAINS(x.name , @fb0)))`},
		},
		{
			input: `foo(count) !~ "^match$"`,
			m: map[string]appendix{
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
				"foo":   {safesql.New("bar"), filtervisitor.String, false},
			},
			output: []string{`(NOT (REGEXP_CONTAINS(bar(x.count) , @fb0)))`},
		},
		{
			input: `color: "blue"`,
			m: map[string]appendix{
				"color": {safesql.New("x.color"), filtervisitor.ArrayType(&filtervisitor.StrConst), false},
			},
			output: []string{`(@fb0 IN x.color)`},
		},
		{
			input: `foo(name)`,
			m: map[string]appendix{
				"foo":  {safesql.New("bar"), filtervisitor.Boolean, false},
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name)`},
		},
		{
			input: `foo(name, "asdf")`,
			m: map[string]appendix{
				"foo":  {safesql.New("bar"), filtervisitor.Boolean, false},
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name, @fb0)`},
		},
		{
			input: `foo(name, bar("asdf"))`,
			m: map[string]appendix{
				"foo":  {safesql.New("bar"), filtervisitor.Boolean, false},
				"bar":  {safesql.New("baz"), filtervisitor.Boolean, false},
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			output: []string{`bar(x.name, baz(@fb0))`},
		},
		{
			input: `bytesfield = "bbb"`,
			m: map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			output: []string{`(x.bytesfield = @fb0)`},
			bindings: map[string]any{
				"fb0": []byte("bbb"),
			},
		},
		{
			input: `bytesfield > "bbb"`,
			m: map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			output: []string{`(x.bytesfield > @fb0)`},
			bindings: map[string]any{
				"fb0": []byte("bbb"),
			},
		},
		{
			input: `bytesfield =~ "^match$"`,
			m: map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
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
			`name = "42"`,
			map[string]appendix{},
		},
		{
			`count > 12 AND stat = "12"`,
			map[string]appendix{
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`len(name) > 12`,
			map[string]appendix{},
		},
		{
			`name: "1"`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`bti`,
			map[string]appendix{},
		},
		{
			`foo(name)`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
		},

		// Type failures.
		{
			`name >= 42`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`state = 123.32`,
			map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
		},
		{
			`count <= "12"`,
			map[string]appendix{
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`name AND count >= 42`,
			map[string]appendix{
				"name":  {safesql.New("x.name"), filtervisitor.String, false},
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`name = "blah" AND count`,
			map[string]appendix{
				"name":  {safesql.New("x.name"), filtervisitor.String, false},
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
			},
		},
		{
			`severity > -10`,
			map[string]appendix{
				"severity": {safesql.New("x.severity"), filtervisitor.Unsigned, false},
			},
		},
		{
			`NOT name`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`humidity`,
			map[string]appendix{
				"humidity": {safesql.New("x.humidity"), filtervisitor.Float, false},
			},
		},
		{
			`color: "blue"`,
			map[string]appendix{
				"color": {safesql.New("x.color"), filtervisitor.ArrayType(&queryfilterbase.IntConst), false},
			},
		},
		{
			`date > "not a date"`,
			map[string]appendix{
				"date": {safesql.New("x.date"), filtervisitor.Date, false},
			},
		},
		{
			`notastring =~ "^match$"`,
			map[string]appendix{
				"notastring": {safesql.New("x.notastring"), filtervisitor.Integer, false},
			},
		},
		{
			`notastring !~ "^match$"`,
			map[string]appendix{
				"notastring": {safesql.New("x.notastring"), filtervisitor.Integer, false},
			},
		},
		{
			`bytesfield = stringfield`,
			map[string]appendix{
				"bytesfield":  {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
				"stringfield": {safesql.New("x.stringfield"), filtervisitor.String, false},
			},
		},
		{
			`bytesfield = 12.0`,
			map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
		},
		{
			`bytesfield = constval`,
			map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
				"constval":   {safesql.New("myconst"), filtervisitor.StrConst, false},
			},
		},
		// The below regex syntax is supported by the parser,
		// but currently we only allow the RHS to be a string literal.
		{
			`name =~ ("foo" AND ("bar" OR "baz"))`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
		},
		{
			`name =~ (NOT "^re")`,
			map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
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
				"str": {safesql.New("y.str"), filtervisitor.String, false},
			},
			output: []string{`(y.str = @fb0)`},
		},
		{
			input: `msg.name = "hello"`,
			m: map[string]appendix{
				"msg.name": {safesql.New("y.msg.name"), filtervisitor.String, false},
			},
			output: []string{`(y.msg.name = @fb0)`},
		},
		{
			input: `recurse.msg.name = "bye"`,
			m: map[string]appendix{
				"recurse.msg.name": {safesql.New("y.recurse.msg.name"), filtervisitor.String, false},
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
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) AND (z.count > @fb1))`},
		},
		{
			input: `"23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((@fb0 = z.name) AND (z.count > @fb1))`},
		},
		{
			input: `name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1))`},
		},
		{
			input: `name = "42" OR count != 42 OR -state=FIRST`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1) OR NOT (z.state = @fb2))`},
		},
		{
			input: `name = "42" AND count = 1 OR count = 2 OR count = 3`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((z.name = @fb0) AND ((z.count = @fb1) OR (z.count = @fb2) OR (z.count = @fb3)))`},
		},
		{
			input: `-name="42"`,
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`NOT (z.name = @fb0)`},
		},
		{
			input: `a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": {safesql.New("z.a"), filtervisitor.Boolean, false},
				"b": {safesql.New("z.b"), filtervisitor.Boolean, false},
				"c": {safesql.New("z.c"), filtervisitor.Boolean, false},
				"d": {safesql.New("z.d"), filtervisitor.Boolean, false},
				"e": {safesql.New("z.e"), filtervisitor.Boolean, false},
			},
			output: []string{`(z.a AND (z.b OR z.c) AND (z.d OR z.e))`},
		},
		{
			input: `(count > 1 AND name !~ "^re") OR name =~ "re$" OR state=FIRST`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`(((z.count > @fb0) AND (NOT (REGEXP_CONTAINS(z.name , @fb1)))) OR (REGEXP_CONTAINS(z.name , @fb2)) OR (z.state = @fb3))`},
		},
		{
			input: `(count > 1 AND name !~ "^re") OR name =~ "re$" OR state=FIRST`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
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
				"name":  {safesql.New("z.name"), filtervisitor.String, true},
				"count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) AND (z.count > @fb1))`},
		},
		{
			input: `"23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, true},
				"count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((@fb0 = z.name) AND (z.count > @fb1))`},
		},
		{
			input: `name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, true},
				"count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count != @fb1))`, `((z.count != @fb0) OR (z.name = @fb1))`},
		},
		{
			input: `name = "42" AND nodes.count = "1" OR nodes.count = "2" OR nodes.count = "3"`,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.name = @fb0) AND ((z.count IN UNNEST (@fb1))))`},
		},
		{
			input: `name = "42" OR nodes.count = "1"`,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count = @fb1))`, `((z.count = @fb0) OR (z.name = @fb1))`},
		},
		{
			input: `name = "42" OR name = "47" OR nodes.count = "1"`,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.String, true},
			},
			output: []string{`((z.count = @fb0) OR (z.name IN UNNEST (@fb1)))`, `((z.name IN UNNEST (@fb0)) OR (z.count = @fb1))`},
		},
		{
			input: `name = "42" OR nodes.count = 1 OR nodes.count = 2 OR nodes.count = 3`,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name = @fb0) OR (z.count IN UNNEST (@fb1)))`, `((z.count IN UNNEST (@fb0)) OR (z.name = @fb1))`},
		},
		{
			input: `nodes.name = "42" OR nodes.name = "44" OR nodes.count = 1 OR nodes.count = 2 OR nodes.count = 3 OR nodes.name = "45"`,
			m: map[string]appendix{
				"nodes.name":  {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			output: []string{`((z.name IN UNNEST (@fb0)) OR (z.count IN UNNEST (@fb1)))`, `((z.count IN UNNEST (@fb0)) OR (z.name IN UNNEST (@fb1)))`},
		},
		{
			input: `-name="42"`,
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, true},
			},
			output: []string{`NOT (z.name = @fb0)`},
		},
		{
			input: `a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": {safesql.New("z.a"), filtervisitor.Boolean, true},
				"b": {safesql.New("z.b"), filtervisitor.Boolean, true},
				"c": {safesql.New("z.c"), filtervisitor.Boolean, true},
				"d": {safesql.New("z.d"), filtervisitor.Boolean, true},
				"e": {safesql.New("z.e"), filtervisitor.Boolean, true},
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
				"kind": {safesql.New("z.kind"), filtervisitor.String, true},
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
				"kind": {safesql.New("z.kind"), filtervisitor.ArrayType(&filtervisitor.String), true},
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
				"kind": {safesql.New("z.kind"), filtervisitor.String, true},
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
				"kind":       {safesql.New("z.kind"), filtervisitor.String, true},
				"other_name": {safesql.New("z.other"), filtervisitor.String, true},
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

			newphrase, ok := tc.sub[phrase]
			if !ok {
				return r
			}
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
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`(SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1))`},
		},
		{
			input: `"full text search" AND name = "42" count > 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) AND (z.count > @fb3)))`},
		},
		{
			input: `"full text search" AND "23" = name AND count > 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND (@fb2 = z.name) AND (z.count > @fb3))`},
		},
		{
			input: `"full text search" AND name = "42" OR count != 42`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) OR (z.count != @fb3)))`},
		},
		{
			input: `"full text search" AND name = "42" OR count != 42 OR -state=FIRST`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND ((z.name = @fb2) OR (z.count != @fb3) OR NOT (z.state = @fb4)))`},
		},
		{
			input: `"full text search" AND name = "42" AND count = 1 OR count = 2 OR count = 3`,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND (z.name = @fb2) AND ((z.count = @fb3) OR (z.count = @fb4) OR (z.count = @fb5)))`},
		},
		{
			input: `"full text search" AND -name="42"`,
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`((SEARCH(TokenizedText, @fb0) OR SEARCH(TokenizedText2, @fb1)) AND NOT (z.name = @fb2))`},
		},
		{
			input: `"full text search" AND a AND b OR c AND d OR e`,
			m: map[string]appendix{
				"a": {safesql.New("z.a"), filtervisitor.Boolean, false},
				"b": {safesql.New("z.b"), filtervisitor.Boolean, false},
				"c": {safesql.New("z.c"), filtervisitor.Boolean, false},
				"d": {safesql.New("z.d"), filtervisitor.Boolean, false},
				"e": {safesql.New("z.e"), filtervisitor.Boolean, false},
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

				if ftsEnabled && err != nil {
					t.Errorf("got: %v, want success", err)
				} else if !ftsEnabled && err == nil {
					t.Errorf("got nil, want error when full text search is disabled")
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
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`(STRPOS(z.name , @fb0) > 0)`},
		},
		{
			input: `name:vrt.43d81b476a52`,
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			output: []string{`(STRPOS(z.name , @fb0) > 0)`},
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
			if err == nil {
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

			if err != nil {
				t.Errorf("got: %v, want success", err)
			}
		})
	}
}

func postgresMemberMap(m map[string]appendix) MemberFunc {
	return func(name []string) (safesql.TrustedSQLString, filtervisitor.PrimType, error) {
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return safesql.New(""), filtervisitor.Invalid, fmt.Errorf("name %q not found", n)
		}
		return a.ql, a.tp, nil
	}
}

func postgresFunctionMap(m map[string]appendix) PostgresFunctionFunc {
	return func(name string, p *Postgres, el []*fpb.Expression) (filtervisitor.PrimType, error) {
		a, ok := m[name]
		if !ok {
			return filtervisitor.Invalid, fmt.Errorf("function %q not found", name)
		}
		p.AppendTrustedSQL(safesql.Concat(a.ql, safesql.New("(")))
		for i, e := range el {
			if i > 0 {
				p.AppendTrustedSQL(safesql.New(", "))
			}
			if _, err := filtervisitor.Visit(p, e); err != nil {
				return filtervisitor.Invalid, err
			}
		}
		p.AppendTrustedSQL(safesql.New(")"))
		return a.tp, nil
	}
}

func postgresDisjunctionMap(m map[string]appendix) DisjunctionFunc {
	return func(name []string) (bool, safesql.TrustedSQLString, error) {
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return false, safesql.New(""), fmt.Errorf("name %q not found", n)
		}
		return a.optimizable, a.ql, nil
	}
}

func TestPostgresSingleLevelAndCoercionSuccess(t *testing.T) {
	parsedTime := time.Date(2012, 11, 1, 22, 8, 41, 0, time.UTC)
	baseSQL := "SELECT x.foo FROM Blah AS x WHERE "

	tests := []struct {
		name    string
		input   string
		m       map[string]appendix
		options []FilterBaseOption
		want    PostgresStatement
	}{
		{
			name:  "string_equality",
			input: `name = "42"`,
			m: map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.name = $1)`,
				Args: []any{"42"},
			},
		},
		{
			name:  "integer_greater_than",
			input: `count > 12`,
			m: map[string]appendix{
				"count": {safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(y.countz > $1)`,
				Args: []any{int64(12)},
			},
		},
		{
			name:  "uint64_coercion_on_integer_field",
			input: `count > 16183100702751628930`,
			m: map[string]appendix{
				"count": {safesql.New("y.countz"), filtervisitor.Integer, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(y.countz > $1)`,
				Args: []any{uint64(16183100702751628930)},
			},
		},
		{
			name:  "unsigned_greater_than",
			input: `severity > 10`,
			m: map[string]appendix{
				"severity": {safesql.New("x.severity"), filtervisitor.Unsigned, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.severity > $1)`,
				Args: []any{int64(10)},
			},
		},
		{
			name:  "float_less_than",
			input: `humidity < 3.14`,
			m: map[string]appendix{
				"humidity": {safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.humidity < $1)`,
				Args: []any{float64(3.14)},
			},
		},
		{
			name:  "float_greater_equals_integer_literal",
			input: `humidity >= 3`,
			m: map[string]appendix{
				"humidity": {safesql.New("x.humidity"), filtervisitor.Float, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.humidity >= $1)`,
				Args: []any{float64(3)},
			},
		},
		{
			name:  "enum_quoted_not_equals",
			input: `state != "FIRST"`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.state != $1)`,
				Args: []any{"FIRST"},
			},
		},
		{
			name:  "enum_unquoted_equals",
			input: `state = FIRST`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.state = $1)`,
				Args: []any{"FIRST"},
			},
		},
		{
			name:  "enum_member_path_coercion",
			input: `state = pkg.FIRST`,
			m: map[string]appendix{
				"state": {safesql.New("x.state"), filtervisitor.Enum, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.state = $1)`,
				Args: []any{"pkg.FIRST"},
			},
		},
		{
			name:  "boolean_unary",
			input: `bit`,
			m: map[string]appendix{
				"bit": {safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			want: PostgresStatement{
				SQL: baseSQL + `x.bit`,
			},
		},
		{
			name:  "boolean_equals_false",
			input: `bit = false`,
			m: map[string]appendix{
				"bit": {safesql.New("x.bit"), filtervisitor.Boolean, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.bit = $1)`,
				Args: []any{false},
			},
		},
		{
			name:  "timestamp_coercion",
			input: `time > "2012-11-01T22:08:41Z"`,
			m: map[string]appendix{
				"time": {safesql.New("x.time"), filtervisitor.Timestamp, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.time > $1)`,
				Args: []any{parsedTime},
			},
		},
		{
			name:  "proto_timestamp_coercion",
			input: `time > "2012-11-01T22:08:41Z"`,
			m: map[string]appendix{
				"time": {safesql.New("x.proto.time.seconds"), filtervisitor.ProtoTimestamp, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.proto.time.seconds > $1)`,
				Args: []any{parsedTime},
			},
		},
		{
			name:  "date_coercion",
			input: `date > "2012-11-01"`,
			m: map[string]appendix{
				"date": {safesql.New("x.date"), filtervisitor.Date, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.date > $1)`,
				Args: []any{"2012-11-01"},
			},
		},
		{
			name:  "regexp_match",
			input: `name =~ "^match$"`,
			m: map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.name ~ $1)`,
				Args: []any{"^match$"},
			},
		},
		{
			name:  "regexp_not_match",
			input: `name !~ "^match$"`,
			m: map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(NOT (x.name ~ $1))`,
				Args: []any{"^match$"},
			},
		},
		{
			name:  "function_regexp_not_match",
			input: `foo(count) !~ "^match$"`,
			m: map[string]appendix{
				"count": {safesql.New("x.count"), filtervisitor.Integer, false},
				"foo":   {safesql.New("bar"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(NOT (bar(x.count) ~ $1))`,
				Args: []any{"^match$"},
			},
		},
		{
			name:  "array_has_operator",
			input: `color: "blue"`,
			m: map[string]appendix{
				"color": {safesql.New("x.color"), filtervisitor.ArrayType(&filtervisitor.StrConst), false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `($1 = ANY(x.color))`,
				Args: []any{"blue"},
			},
		},
		{
			name:    "string_has_operator_with_strpos",
			input:   `name : 42`,
			options: []FilterBaseOption{WithHasOperatorAsContains},
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(STRPOS(z.name , $1) > 0)`,
				Args: []any{"42"},
			},
		},
		{
			name:    "string_has_operator_with_member_path_rhs",
			input:   `name:vrt.43d81b476a52`,
			options: []FilterBaseOption{WithHasOperatorAsContains},
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(STRPOS(z.name , $1) > 0)`,
				Args: []any{"vrt.43d81b476a52"},
			},
		},
		{
			name:  "nested_function_call",
			input: `foo(name, bar("asdf"))`,
			m: map[string]appendix{
				"foo":  {safesql.New("bar"), filtervisitor.Boolean, false},
				"bar":  {safesql.New("baz"), filtervisitor.Boolean, false},
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `bar(x.name, baz($1))`,
				Args: []any{"asdf"},
			},
		},
		{
			name:  "bytes_equality",
			input: `bytesfield = "bbb"`,
			m: map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.bytesfield = $1)`,
				Args: []any{[]byte("bbb")},
			},
		},
		{
			name:  "bytes_regexp_match",
			input: `bytesfield =~ "^match$"`,
			m: map[string]appendix{
				"bytesfield": {safesql.New("x.bytesfield"), filtervisitor.Bytes, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(x.bytesfield ~ $1)`,
				Args: []any{[]byte("^match$")},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			exp, err := filter.ParseExpressionWithOptions(options, tc.input)
			if err != nil {
				t.Fatalf("ParseExpressionWithOptions(%q): %v", tc.input, err)
			}
			query := NewPostgresStatement(baseSQL)
			f := Postgres{
				MemberFunc:        postgresMemberMap(tc.m),
				FunctionFunc:      postgresFunctionMap(tc.m),
				Q:                 &query,
				FilterBaseOptions: tc.options,
			}
			if err := f.AppendFilters(exp); err != nil {
				t.Fatalf("AppendFilters(%q): %v", tc.input, err)
			}
			if diff := cmp.Diff(tc.want, query); diff != "" {
				t.Errorf("PostgresStatement mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostgresComplexAndOptimizedDisjunctions(t *testing.T) {
	baseSQL := "SELECT x.foo FROM Blah AS x WHERE "

	tests := []struct {
		name      string
		input     string
		optimized bool
		m         map[string]appendix
		want      PostgresStatement
	}{
		{
			name:      "conjunction_and_positional_bindings",
			input:     `name = "42" count > 42`,
			optimized: false,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((z.name = $1) AND (z.count > $2))`,
				Args: []any{"42", int64(42)},
			},
		},
		{
			name:      "inverted_lhs_literal_equality",
			input:     `"23" = name AND count > 42`,
			optimized: false,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(($1 = z.name) AND (z.count > $2))`,
				Args: []any{"23", int64(42)},
			},
		},
		{
			name:      "disjunction_with_negation",
			input:     `name = "42" OR count != 42 OR -state=FIRST`,
			optimized: false,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((z.name = $1) OR (z.count != $2) OR NOT (z.state = $3))`,
				Args: []any{"42", int64(42), "FIRST"},
			},
		},
		{
			name:      "complex_regexp_and_not_regexp_disjunction",
			input:     `(count > 1 AND name !~ "^re") OR name =~ "re$" OR state=FIRST`,
			optimized: false,
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
				"state": {safesql.New("z.state"), filtervisitor.Enum, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(((z.count > $1) AND (NOT (z.name ~ $2))) OR (z.name ~ $3) OR (z.state = $4))`,
				Args: []any{int64(1), "^re", "re$", "FIRST"},
			},
		},
		{
			name:      "optimized_disjunction_single_group",
			input:     `name = "42" AND nodes.count = "1" OR nodes.count = "2" OR nodes.count = "3"`,
			optimized: true,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.String, true},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((z.name = $1) AND ((z.count = ANY($2))))`,
				Args: []any{"42", []string{"1", "2", "3"}},
			},
		},
		{
			name:      "optimized_disjunction_mixed_with_scalar",
			input:     `name = "42" OR name = "47" OR nodes.count = "1"`,
			optimized: true,
			m: map[string]appendix{
				"name":        {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.String, true},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((z.name = ANY($1)) OR (z.count = $2))`,
				Args: []any{[]string{"42", "47"}, "1"},
			},
		},
		{
			name:      "optimized_disjunction_multiple_groups",
			input:     `nodes.name = "42" OR nodes.name = "44" OR nodes.count = 1 OR nodes.count = 2 OR nodes.count = 3 OR nodes.name = "45"`,
			optimized: true,
			m: map[string]appendix{
				"nodes.name":  {safesql.New("z.name"), filtervisitor.String, true},
				"nodes.count": {safesql.New("z.count"), filtervisitor.Integer, true},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((z.name = ANY($1)) OR (z.count = ANY($2)))`,
				Args: []any{[]string{"42", "44", "45"}, []string{"1", "2", "3"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			exp, err := filter.ParseExpressionWithOptions(options, tc.input)
			if err != nil {
				t.Fatalf("ParseExpressionWithOptions(%q): %v", tc.input, err)
			}
			query := NewPostgresStatement(baseSQL)
			f := Postgres{
				MemberFunc:          postgresMemberMap(tc.m),
				FunctionFunc:        postgresFunctionMap(tc.m),
				DisjunctionFunc:     postgresDisjunctionMap(tc.m),
				OptimizeDisjunction: tc.optimized,
				Q:                   &query,
			}
			if err := f.AppendFilters(exp); err != nil {
				t.Fatalf("AppendFilters(%q): %v", tc.input, err)
			}
			if diff := cmp.Diff(tc.want, query); diff != "" {
				t.Errorf("PostgresStatement mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostgresFullTextSearch(t *testing.T) {
	baseSQL := "SELECT x.foo FROM Blah AS x WHERE "
	pgSearchCols := []safesql.TrustedSQLString{safesql.New("Title_Tokens"), safesql.New("Tags_Tokens")}

	tests := []struct {
		name      string
		input     string
		m         map[string]appendix
		searchCol []safesql.TrustedSQLString
		options   []FilterBaseOption
		want      PostgresStatement
		wantErr   bool
	}{
		{
			name:      "phrase_full_text_search",
			input:     `"full text search"`,
			searchCol: pgSearchCols,
			options:   []FilterBaseOption{WithFullTextSearchEnabled},
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `(Title_Tokens @@ plainto_tsquery('simple', $1) OR Tags_Tokens @@ plainto_tsquery('simple', $2))`,
				Args: []any{"full text search", "full text search"},
			},
		},
		{
			name:      "full_text_search_combined_with_restrictions",
			input:     `"full text search" AND name = "42" count > 42`,
			searchCol: pgSearchCols,
			options:   []FilterBaseOption{WithFullTextSearchEnabled},
			m: map[string]appendix{
				"name":  {safesql.New("z.name"), filtervisitor.String, false},
				"count": {safesql.New("z.count"), filtervisitor.Integer, false},
			},
			want: PostgresStatement{
				SQL:  baseSQL + `((Title_Tokens @@ plainto_tsquery('simple', $1) OR Tags_Tokens @@ plainto_tsquery('simple', $2)) AND ((z.name = $3) AND (z.count > $4)))`,
				Args: []any{"full text search", "full text search", "42", int64(42)},
			},
		},
		{
			name:      "full_text_search_disabled_returns_error",
			input:     `"full text search"`,
			searchCol: pgSearchCols,
			m: map[string]appendix{
				"name": {safesql.New("z.name"), filtervisitor.String, false},
			},
			wantErr: true,
		},
		{
			name:    "full_text_search_missing_columns_returns_error",
			input:   `"full text search"`,
			options: []FilterBaseOption{WithFullTextSearchEnabled},
			wantErr: true,
		},
		{
			name:      "full_text_search_empty_string_returns_error",
			input:     `""`,
			searchCol: pgSearchCols,
			options:   []FilterBaseOption{WithFullTextSearchEnabled},
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			options := fpb.ExpressionParserOptions_builder{EnableRegexpMatching: true}.Build()
			exp, err := filter.ParseExpressionWithOptions(options, tc.input)
			if err != nil {
				t.Fatalf("ParseExpressionWithOptions(%q): %v", tc.input, err)
			}
			query := NewPostgresStatement(baseSQL)
			f := Postgres{
				MemberFunc:        postgresMemberMap(tc.m),
				Q:                 &query,
				TextSearchColumns: tc.searchCol,
				FilterBaseOptions: tc.options,
			}
			err = f.AppendFilters(exp)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("AppendFilters(%q) succeeded with %q, want error", tc.input, query.SQL)
				}
				return
			}
			if err != nil {
				t.Fatalf("AppendFilters(%q): %v", tc.input, err)
			}
			if diff := cmp.Diff(tc.want, query); diff != "" {
				t.Errorf("PostgresStatement mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostgresRestrictionTranslateAndFailures(t *testing.T) {
	t.Run("restriction_translate_rewrites_array_has", func(t *testing.T) {
		exp, err := filter.ParseExpression(`kind : "FOO_OLD"`)
		if err != nil {
			t.Fatalf("ParseExpression: %v", err)
		}
		query := NewPostgresStatement("SELECT x.f FROM b AS x WHERE ")
		f := Postgres{
			MemberFunc: postgresMemberMap(map[string]appendix{
				"kind": {safesql.New("z.kind"), filtervisitor.ArrayType(&filtervisitor.String), true},
			}),
			Q: &query,
			RestrictionTranslate: func(r *fpb.Restriction) *fpb.Restriction {
				if r.GetRhs().GetPhrase().GetValues()[0] != "FOO_OLD" {
					return r
				}
				out := &fpb.Restriction{}
				proto.Merge(out, r)
				out.GetRhs().GetPhrase().GetValues()[0] = "FOO_NEW"
				return out
			},
		}
		if err := f.AppendFilters(exp); err != nil {
			t.Fatalf("AppendFilters: %v", err)
		}
		want := PostgresStatement{
			SQL:  "SELECT x.f FROM b AS x WHERE ($1 = ANY(z.kind))",
			Args: []any{"FOO_NEW"},
		}
		if diff := cmp.Diff(want, query); diff != "" {
			t.Errorf("PostgresStatement mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("unregistered_function_returns_error", func(t *testing.T) {
		exp, err := filter.ParseExpression(`foo(name)`)
		if err != nil {
			t.Fatalf("ParseExpression: %v", err)
		}
		query := NewPostgresStatement("SELECT x.f FROM b AS x WHERE ")
		f := Postgres{
			MemberFunc: postgresMemberMap(map[string]appendix{
				"name": {safesql.New("x.name"), filtervisitor.String, false},
			}),
			Q: &query,
		}
		if err := f.AppendFilters(exp); err == nil {
			t.Errorf("AppendFilters succeeded with %q, want error when FunctionFunc is nil", query.SQL)
		}
	})
}
