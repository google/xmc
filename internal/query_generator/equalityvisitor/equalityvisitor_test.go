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
package equalityvisitor

import (
	"fmt"
	"strings"
	"testing"

	fpb "xmanager_cloud/api/filter"
	"xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/api/filter/go/filtervisitor"
)

type testCase struct {
	input    string
	m        map[string]testMember
	expected map[string]bool
}

type testMember struct {
	tf string
	tp filtervisitor.PrimType
}

func memberMap(equalMembers *map[string]bool, m map[string]testMember) MemberFunc {
	return func(name []string, followsEq bool) (filtervisitor.PrimType, error) {
		if !followsEq {
			return filtervisitor.Invalid, nil
		}
		n := strings.Join(name, ".")
		a, ok := m[n]
		if !ok {
			return filtervisitor.Invalid, fmt.Errorf("name %q not found", n)
		}
		(*equalMembers)[a.tf] = true
		return a.tp, nil
	}
}

func TestEquality(t *testing.T) {
	tests := []testCase{
		{
			`name = "42"`,
			map[string]testMember{
				"name": {"name", filtervisitor.String},
			},
			map[string]bool{"name": true},
		},
		{
			`count > 12`,
			map[string]testMember{
				"count": {"count", filtervisitor.Integer},
			},
			map[string]bool{},
		},
		{
			`humidity < 3.14`,
			map[string]testMember{
				"humidity": {"humidity", filtervisitor.Integer},
			},
			map[string]bool{},
		},
		{
			`humidity >= 3`,
			map[string]testMember{
				"humidity": {"humidity", filtervisitor.Integer},
			},
			map[string]bool{},
		},
		{
			`state != "FIRST"`,
			map[string]testMember{
				"state": {"state", filtervisitor.Integer},
			},
			map[string]bool{},
		},
		{
			`bit`,
			map[string]testMember{
				"bit": {"bit", filtervisitor.Boolean},
			},
			map[string]bool{},
		},
		{
			`bit = false`,
			map[string]testMember{
				"bit": {"bit", filtervisitor.Boolean},
			},
			map[string]bool{"bit": true},
		},
		{
			`severity > 10`,
			map[string]testMember{
				"severity": {"severity", filtervisitor.Unsigned},
			},
			map[string]bool{},
		},
		{
			`time > "2012-11-01T22:08:41+00:00"`,
			map[string]testMember{
				"time": {"time", filtervisitor.Timestamp},
			},
			map[string]bool{},
		},
		{
			`foo(name)`,
			map[string]testMember{
				"foo":  {"bar", filtervisitor.Boolean},
				"name": {"name", filtervisitor.String},
			},
			map[string]bool{},
		},
		{
			`foo(name, "asdf")`,
			map[string]testMember{
				"foo":  {"bar", filtervisitor.Boolean},
				"name": {"name", filtervisitor.String},
			},
			map[string]bool{},
		},
		{
			`foo(name, bar("asdf"))`,
			map[string]testMember{
				"foo":  {"bar", filtervisitor.Boolean},
				"bar":  {"baz", filtervisitor.Boolean},
				"name": {"name", filtervisitor.String},
			},
			map[string]bool{},
		},
		{
			`name = "42" count > 42`,
			map[string]testMember{
				"name":  {"name", filtervisitor.String},
				"count": {"count", filtervisitor.Integer},
			},
			map[string]bool{"name": true},
		},
		{
			`name = "42" OR count > 42`,
			map[string]testMember{
				"name":  {"name", filtervisitor.String},
				"count": {"count", filtervisitor.Integer},
			},
			map[string]bool{},
		},
		{
			`name = "42" AND count > 42`,
			map[string]testMember{
				"name":  {"name", filtervisitor.String},
				"count": {"count", filtervisitor.Integer},
			},
			map[string]bool{"name": true},
		},
		{
			`name = "42" AND count = 42`,
			map[string]testMember{
				"name":  {"name", filtervisitor.String},
				"count": {"count", filtervisitor.Integer},
			},
			map[string]bool{"name": true,
				"count": true},
		},
	}

	for _, tc := range tests {
		exp, err := filter.ParseExpression(tc.input)
		if err != nil {
			t.Errorf("ParseExpression(%q): %v", tc.input, err)
			continue
		}
		m := map[string]bool{}
		t.Log("calling into EqualityVisitor")
		ev := EqualityVisitor{
			MemberFunc: memberMap(&m, tc.m),
			FunctionFunc: func(functionName string, ev *EqualityVisitor, args []*fpb.Expression) (filtervisitor.PrimType, error) {
				old := ev.visitFromEqual
				ev.visitFromEqual = false
				for _, arg := range args {
					if _, err := filtervisitor.Visit(ev, arg); err != nil {
						ev.visitFromEqual = old
						return filtervisitor.Invalid, err
					}
				}
				ev.visitFromEqual = old
				return filtervisitor.Invalid, nil
			},
		}
		if _, err := filtervisitor.Visit(&ev, exp); err != nil {
			t.Errorf("Visit(%v) failed with error: %v", exp, err)
		}
		if len(m) != len(tc.expected) {
			t.Errorf("exp %v len(m) got: %v, wanted %v", exp, len(m), len(tc.expected))
		}
		for k := range m {
			if ok, _ := tc.expected[k]; !ok {
				t.Errorf("got: %v, want: %v", m, tc.expected)
			}
		}
	}
}
