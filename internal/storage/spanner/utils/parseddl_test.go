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
package spannerutils

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseDDL(t *testing.T) {
	tests := []struct {
		name string
		ddl  string
		want []string
	}{
		{
			name: "empty",
			ddl:  "",
			want: nil,
		},
		{
			name: "single statement",
			ddl:  "CREATE TABLE t (k INT64) PRIMARY KEY (k);",
			want: []string{"CREATE TABLE t (k INT64) PRIMARY KEY (k)"},
		},
		{
			name: "multiple statements",
			ddl:  "CREATE TABLE t1 (k INT64) PRIMARY KEY (k); CREATE TABLE t2 (k INT64) PRIMARY KEY (k)",
			want: []string{
				"CREATE TABLE t1 (k INT64) PRIMARY KEY (k)",
				"CREATE TABLE t2 (k INT64) PRIMARY KEY (k)",
			},
		},
		{
			name: "statements with newlines and comments",
			ddl: `
				-- This is a comment
				CREATE TABLE t1 (
					k INT64
				) PRIMARY KEY (k);

				-- Another comment
				CREATE TABLE t2 (
					k INT64
				) PRIMARY KEY (k);
			`,
			want: []string{
				"CREATE TABLE t1 (\n\t\t\t\t\tk INT64\n\t\t\t\t) PRIMARY KEY (k)",
				"CREATE TABLE t2 (\n\t\t\t\t\tk INT64\n\t\t\t\t) PRIMARY KEY (k)",
			},
		},
		{
			name: "comments only",
			ddl:  "-- comment 1; -- comment 2",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDDL(tt.ddl)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseDDL() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
