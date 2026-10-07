// Copyright 2026 Google LLC
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

package safesql

import "testing"

func TestNewAndString(t *testing.T) {
	tests := []struct {
		name string
		sql  TrustedSQLString
		want string
	}{
		{
			name: "select query constant",
			sql:  New("SELECT * FROM Experiments WHERE Xid = @xid"),
			want: "SELECT * FROM Experiments WHERE Xid = @xid",
		},
		{
			name: "empty string helper",
			sql:  EmptyString(),
			want: "",
		},
		{
			name: "zero value",
			sql:  TrustedSQLString{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sql.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConcat(t *testing.T) {
	tests := []struct {
		name  string
		parts []TrustedSQLString
		want  string
	}{
		{
			name:  "no parts",
			parts: nil,
			want:  "",
		},
		{
			name:  "single part",
			parts: []TrustedSQLString{New("SELECT 1")},
			want:  "SELECT 1",
		},
		{
			name: "multiple parts",
			parts: []TrustedSQLString{
				New("SELECT "),
				New("e.Xid"),
				New(" FROM Experiments e"),
			},
			want: "SELECT e.Xid FROM Experiments e",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Concat(tt.parts...).String(); got != tt.want {
				t.Errorf("Concat() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStringJoin(t *testing.T) {
	tests := []struct {
		name  string
		parts []TrustedSQLString
		want  string
	}{
		{
			name:  "empty slice",
			parts: nil,
			want:  "",
		},
		{
			name:  "single element",
			parts: []TrustedSQLString{New("e.Xid")},
			want:  "e.Xid",
		},
		{
			name: "multiple elements",
			parts: []TrustedSQLString{
				New("e.Xid"),
				New("e.Title"),
				New("e.CreateTime"),
			},
			want: "e.Xid, e.Title, e.CreateTime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringJoin(", ", tt.parts...).String(); got != tt.want {
				t.Errorf("StringJoin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewBindingParam(t *testing.T) {
	tests := []struct {
		name  string
		index uint64
		want  string
	}{
		{
			name:  "zero index",
			index: 0,
			want:  "@fb0",
		},
		{
			name:  "non-zero index",
			index: 42,
			want:  "@fb42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewBindingParam("@fb", tt.index).String(); got != tt.want {
				t.Errorf("NewBindingParam() = %q, want %q", got, tt.want)
			}
		})
	}
}
