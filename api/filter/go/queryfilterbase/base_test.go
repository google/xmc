// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package queryfilterbase

import (
	"testing"
	"time"

	fpb "xmanager_cloud/api/filter"
)

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantQuote bool
	}{
		{name: "empty", in: "", want: "", wantQuote: false},
		{name: "single_double_quote", in: `"`, want: `"`, wantQuote: false},
		{name: "single_apostrophe", in: `'`, want: `'`, wantQuote: false},
		{name: "empty_double_quoted", in: `""`, want: "", wantQuote: true},
		{name: "empty_single_quoted", in: `''`, want: "", wantQuote: true},
		{name: "double_quoted", in: `"abc"`, want: "abc", wantQuote: true},
		{name: "single_quoted", in: `'abc'`, want: "abc", wantQuote: true},
		{name: "mismatched_quotes", in: `"abc'`, want: `"abc'`, wantQuote: false},
		{name: "multibyte_utf8", in: `"日本語"`, want: "日本語", wantQuote: true},
		{name: "inner_quote", in: `'a"b'`, want: `a"b`, wantQuote: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := stripQuotes(tc.in)
			if got != tc.want || ok != tc.wantQuote {
				t.Errorf("stripQuotes(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantQuote)
			}
		})
	}
}

func TestParseTimestampVal(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantOK   bool
		wantTime time.Time
	}{
		{name: "empty", in: "", wantOK: false},
		{name: "rfc3339", in: "2026-01-02T03:04:05Z", wantOK: true, wantTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{name: "date_only", in: "2026-01-02", wantOK: true, wantTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{name: "single_quoted_rejected", in: "'2026-01-02'", wantOK: false},
		{name: "invalid", in: "not-a-timestamp", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseTimestampVal(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("parseTimestampVal(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if tc.wantOK && !got.Equal(tc.wantTime) {
				t.Errorf("parseTimestampVal(%q) time = %v, want %v", tc.in, got, tc.wantTime)
			}
		})
	}
}

func TestRegexpLiteral(t *testing.T) {
	tests := []struct {
		name    string
		in      *fpb.Restriction
		want    string
		wantErr bool
	}{
		{name: "nil_restriction", in: nil, wantErr: true},
		{
			name:    "non_regexp_comparator",
			in:      &fpb.Restriction{Comparator: fpb.Restriction_EQUALS},
			wantErr: true,
		},
		{
			name: "quoted_value",
			in: &fpb.Restriction{
				Comparator: fpb.Restriction_REGEXP_MATCH,
				Rhs:        &fpb.Expression{Expression: &fpb.Expression_Value{Value: `"^foo.*"`}},
			},
			want: "^foo.*",
		},
		{
			name: "single_quote_value_no_panic",
			in: &fpb.Restriction{
				Comparator: fpb.Restriction_REGEXP_NOT_MATCH,
				Rhs:        &fpb.Expression{Expression: &fpb.Expression_Value{Value: `"`}},
			},
			want: `"`,
		},
		{
			name: "phrase_value",
			in: &fpb.Restriction{
				Comparator: fpb.Restriction_REGEXP_MATCH,
				Rhs:        &fpb.Expression{Expression: &fpb.Expression_Phrase{Phrase: &fpb.Phrase{Values: []string{"foo", "bar"}}}},
			},
			want: "foo bar",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RegexpLiteral(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RegexpLiteral() err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("RegexpLiteral() = %q, want %q", got, tc.want)
			}
		})
	}
}
