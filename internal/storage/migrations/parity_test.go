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

package migrations

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	pgversion "xmanager_cloud/internal/storage/postgres/migrations/version"
	spannerversion "xmanager_cloud/internal/storage/spanner/migrations/version"
)

var createTableRe = regexp.MustCompile(`(?im)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_]+)`)

func TestSchemaVersionParity(t *testing.T) {
	tests := []struct {
		name           string
		spannerVersion int64
		pgVersion      int64
	}{
		{
			name:           "ess",
			spannerVersion: spannerversion.EssExpectedSchemaVersion(),
			pgVersion:      pgversion.EssExpectedSchemaVersion(),
		},
		{
			name:           "xid",
			spannerVersion: spannerversion.XidExpectedSchemaVersion(),
			pgVersion:      pgversion.XidExpectedSchemaVersion(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.spannerVersion != tc.pgVersion {
				t.Errorf("schema version mismatch: spanner=%d, postgres=%d", tc.spannerVersion, tc.pgVersion)
			}
		})
	}
}

func TestSchemaTablesParity(t *testing.T) {
	tests := []struct {
		name          string
		spannerSchema string
		pgSchema      string
	}{
		{
			name:          "ess",
			spannerSchema: "../spanner/schema/ess-schema.sql",
			pgSchema:      "../postgres/schema/ess-schema.sql",
		},
		{
			name:          "xid",
			spannerSchema: "../spanner/schema/xid-schema.sql",
			pgSchema:      "../postgres/schema/xid-schema.sql",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spannerTables := mustExtractTables(t, tc.spannerSchema)
			pgTables := mustExtractTables(t, tc.pgSchema)
			if diff := cmp.Diff(spannerTables, pgTables); diff != "" {
				t.Errorf("schema tables mismatch (-spanner +postgres):\n%s", diff)
			}
		})
	}
}

func mustExtractTables(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", path, err)
	}
	matches := createTableRe.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no CREATE TABLE statements found in %q", path)
	}
	tables := make([]string, 0, len(matches))
	for _, m := range matches {
		tables = append(tables, strings.ToLower(m[1]))
	}
	sort.Strings(tables)
	return tables
}
