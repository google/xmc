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
package normalizer

import (
	"testing"

	"xmanager_cloud/api/filter/go/filtervisitor"
	"xmanager_cloud/internal/query_generator/querytools"
)

var experimentsTableView = querytools.TableView{
	Fields: map[querytools.TableField]querytools.Member{
		"bit_reversed_xid": {
			Name: "bit_reversed_xid",
			Type: filtervisitor.Integer,
		},
		"xid": {
			Name: "xid",
			Type: filtervisitor.Integer,
		},
		"title": {
			Name: "title",
			Type: filtervisitor.String,
		},
		"creator": {
			Name: "creator",
			Type: filtervisitor.String,
		},
		"priority": {
			Name: "priority",
			Type: filtervisitor.Integer,
		},
		"urls": {
			Name: "urls",
			Type: filtervisitor.ArrayType(&filtervisitor.Bytes),
		},
		"tags": {
			Name: "tags",
			Type: filtervisitor.ArrayType(&filtervisitor.String),
		},
		"notes": {
			Name: "notes",
			Type: filtervisitor.String,
		},
		"resources": {
			Name: "resources",
			Type: filtervisitor.Bytes,
		},
		"status": {
			Name: "status",
			Type: filtervisitor.Bytes,
		},
		"active_state": {
			Name: "active_state",
			Type: filtervisitor.String,
		},
		"launch_state": {
			Name: "launch_state",
			Type: filtervisitor.String,
		},
		"created": {
			Name: "created",
			Type: filtervisitor.Timestamp,
		},
		"updated": {
			Name: "updated",
			Type: filtervisitor.Timestamp,
		},
		"completed": {
			Name: "completed",
			Type: filtervisitor.Timestamp,
		},
	},
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// --- Basic Type Handling ---
		{
			name:     "Integer: clean input",
			input:    "priority = 10",
			expected: "priority=10",
		},
		{
			name:     "Integer: strip unnecessary quotes",
			input:    `priority = "10"`,
			expected: "priority=10",
		},
		{
			name:     "String: add missing quotes",
			input:    "title = hello",
			expected: `title="hello"`,
		},
		{
			name:     "String: keep existing quotes",
			input:    `title = "hello world"`,
			expected: `title="hello world"`,
		},
		{
			name:     "String: numeric value treated as string",
			input:    "title = 123",
			expected: `title="123"`,
		},
		{
			name:     "Boolean keyword: literals should not be quoted",
			input:    "active_state = true",
			expected: "active_state=true",
		},
		{
			name:     "Null Handling",
			input:    "creator = null",
			expected: "creator=null",
		},

		// --- Operators & Logic ---
		{
			name:     "Inequality Operators",
			input:    "priority >= 5",
			expected: "priority>=5",
		},
		{
			name:     "Not equals operator",
			input:    "xid != 1",
			expected: "xid!=1",
		},
		{
			name:     "Less than or equal operator",
			input:    "xid <= 1",
			expected: "xid<=1",
		},
		{
			name:     "Has Operator (:) for arrays",
			input:    "tags:production",
			expected: `tags:"production"`,
		},
		{
			name:     "Has Operator (:) for arrays with numbers",
			input:    "tags:123",
			expected: `tags:"123"`,
		},
		{
			name:     "Negation operator",
			input:    "-xid=1",
			expected: "-xid=1",
		},
		{
			name:     "Negation on array field with colon",
			input:    "-tags:foo",
			expected: `NOT tags:"foo"`,
		},
		{
			name:     "Negation on array field with colon and quotes",
			input:    `-tags:"foo bar"`,
			expected: `NOT tags:"foo bar"`,
		},
		{
			name:     "Negation on array field with colon and number",
			input:    "-tags:123",
			expected: `NOT tags:"123"`,
		},
		{
			name:     "Composite negation",
			input:    `-tags:"foo bar" -tags:123 -xid=1 -priority=10`,
			expected: `NOT tags:"foo bar" NOT tags:"123" -xid=1 -priority=10`,
		},
		{
			name:     "Colon instead of equals for string field",
			input:    "creator:test",
			expected: `creator="test"`,
		},
		{
			name:     "Colon instead of equals for integer field",
			input:    "xid:1",
			expected: "xid=1",
		},
		{
			name:     "Colon instead of equals for quoted string field",
			input:    `title:"foo bar"`,
			expected: `title="foo bar"`,
		},
		{
			name:     "Colon instead of equals for unquoted string",
			input:    "notes:test",
			expected: `notes="test"`,
		},
		{
			name:     "Composite colon handling with array and non-array",
			input:    "tags:foo creator:test",
			expected: `tags:"foo" creator="test"`,
		},
		{
			name:     "Composite colon handling with list and non-array",
			input:    "status:(a b) creator:test",
			expected: `status:("a" "b") creator="test"`,
		},
		{
			name:     "Composite colon handling with array, list, and non-array",
			input:    "tags:foo status:(a b) creator:test",
			expected: `tags:"foo" status:("a" "b") creator="test"`,
		},
		{
			name:     "Unary minus inside parentheses",
			input:    "(-xid=1)",
			expected: "(-xid=1)",
		},
		{
			name:     "Logical AND",
			input:    "priority=1 AND title=foo",
			expected: `priority=1 AND title="foo"`,
		},
		{
			name:     "Logical OR",
			input:    "active_state=true OR xid < 5",
			expected: "active_state=true OR xid<5",
		},
		{
			name:     "Logical NOT",
			input:    "NOT active_state=false",
			expected: "NOT active_state=false",
		},

		// --- Case Sensitivity (Case-Insensitive Logic) ---
		{
			name:     "Case Sensitivity (LowerCase AND)",
			input:    "status=error and priority=10",
			expected: `status="error" AND priority=10`,
		},
		{
			name:     "Case Sensitivity (Mixed Case OR)",
			input:    "active_state=true Or xid<5",
			expected: "active_state=true OR xid<5",
		},
		{
			name:     "Case Sensitivity (LowerCase NOT)",
			input:    "not active_state=false",
			expected: "NOT active_state=false",
		},
		{
			name:     "Complex Case Insensitive Logic",
			input:    "title=foo aNd tags:bar oR Not priority=10",
			expected: `title="foo" AND tags:"bar" OR NOT priority=10`,
		},

		// --- Global Restrictions (Free Floating Text) ---
		{
			name:     "Single global search term",
			input:    "running",
			expected: `"running"`,
		},
		{
			name:     "Multiple global search terms",
			input:    "running test",
			expected: `"running" "test"`,
		},
		{
			name:     "Mixed field and global",
			input:    "status=error dangerous",
			expected: `status="error" "dangerous"`,
		},
		{
			name:     "Mixed field and multi-line global",
			input:    "status=error dangerous \"multi line global\"",
			expected: `status="error" "dangerous" "multi line global"`,
		},
		{
			name:     "Keywords should not be quoted",
			input:    "status=error and dangerous \"an unquoted and and or not\"",   //nolint:dupword // intentional repeated keyword inside quoted phrase
			expected: `status="error" AND "dangerous" "an unquoted and and or not"`, //nolint:dupword // intentional repeated keyword inside quoted phrase
		},
		{
			name:     "Quoted keyword is treated as string",
			input:    `"and"`,
			expected: `"and"`,
		},

		// --- Timestamps & Complex Strings ---
		{
			name:     "Timestamp (ISO8601)",
			input:    "created = 2023-01-01T12:00:00Z",
			expected: `created="2023-01-01T12:00:00Z"`,
		},
		{
			name:     "UUIDs / Hyphenated Strings",
			input:    "creator = 123e4567-e89b-12d3-a456-426614174000",
			expected: `creator="123e4567-e89b-12d3-a456-426614174000"`,
		},

		// --- Grouping (Parentheses) ---
		{
			name:     "Simple grouping",
			input:    "(priority=1)",
			expected: "(priority=1)",
		},
		{
			name:     "Complex grouping",
			input:    "active_state=true AND (tags:urgent OR priority > 10)",
			expected: `active_state=true AND (tags:"urgent" OR priority>10)`,
		},
		{
			name:     "Empty Groups",
			input:    "()",
			expected: "()",
		},
		{
			name:     "Adjacent Strings (Implicit Lists)",
			input:    "status: (active pending)",
			expected: `status:("active" "pending")`,
		},

		// --- Functions ---
		{
			name:     "Function Call Syntax",
			input:    `has(tags, "prod")`,
			expected: `has(tags,"prod")`,
		},

		// --- Edge Cases ---
		{
			name:     "Negative numbers",
			input:    "xid = -5",
			expected: "xid=-5",
		},
		{
			name:     "Minus sign in text",
			input:    "title = pre-prod",
			expected: `title="pre-prod"`,
		},
		{
			name:     "Unicode & Emoji",
			input:    "tags:🚀",
			expected: `tags:"🚀"`,
		},
		{
			name:     "Escaped Quotes in String",
			input:    `notes = "contains \"quoted\" text"`,
			expected: `notes="contains \"quoted\" text"`,
		},
		{
			name:     "Dot Notation in Field",
			input:    `metadata.labels.env = prod`,
			expected: `metadata.labels.env="prod"`,
		},
		{
			name:     "Scientific Notation",
			input:    "priority > 1.5e3",
			expected: "priority>1.5e3",
		},
		{
			name:     "Scientific Notation Negative Exponent",
			input:    "priority > 1.5e-3",
			expected: "priority>1.5e-3",
		},
		{
			name:     "Whitespace Variations (Tabs/Newlines)",
			input:    "priority=1\nOR\tactive_state=true",
			expected: "priority=1 OR active_state=true",
		},
		{
			name:     "Leading Zeros in Strings",
			input:    "title=007",
			expected: `title="007"`,
		},
		{
			name:     "Leading Zeros in Integers",
			input:    "xid=005",
			expected: "xid=005",
		},
		{
			name:     "Wildcards in Values",
			input:    "title=test*",
			expected: `title="test*"`,
		},
		{
			name:     "Operators Inside Quoted Strings",
			input:    `notes="a=b"`,
			expected: `notes="a=b"`,
		},
		{
			name:     "Single Quotes",
			input:    "title='O'Reilly'",
			expected: `title="'O'Reilly'"`,
		},
		{
			name:     "Case-Insensitive Field Lookup (Mismatch)",
			input:    "Title=hello",
			expected: `Title="hello"`,
		},
		{
			name:     "Empty input",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.input, experimentsTableView)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got != tc.expected {
				t.Errorf("Normalize(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
