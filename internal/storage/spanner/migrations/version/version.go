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

// Package version provides the current schema migration versions for ESS and XID.
package version

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

// Those values are set dynamically during BAZEL build.
var (
	essMigrationVersion int64
	xidMigrationVersion int64
)

// EssExpectedSchemaVersion returns the expected schema version for ESS.
func EssExpectedSchemaVersion() int64 {
	return essMigrationVersion
}

// XidExpectedSchemaVersion returns the expected schema version for XID.
func XidExpectedSchemaVersion() int64 {
	return xidMigrationVersion
}

// QueryCurrentVersion queries the SchemaMigrations table and returns the current version and dirty status.
func QueryCurrentVersion(ctx context.Context, sc *spanner.Client) (int64, bool, error) {
	if sc == nil {
		return 0, false, fmt.Errorf("spanner client is nil")
	}

	stmt := spanner.Statement{
		SQL: "SELECT Version, Dirty FROM SchemaMigrations LIMIT 1",
	}
	iter := sc.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err == iterator.Done {
		return 0, false, fmt.Errorf("no migration records found in SchemaMigrations table")
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to query SchemaMigrations: %w", err)
	}

	var version spanner.NullInt64
	var dirty spanner.NullBool
	if err := row.Columns(&version, &dirty); err != nil {
		return 0, false, fmt.Errorf("failed to parse SchemaMigrations columns: %w", err)
	}

	return version.Int64, !dirty.Valid || dirty.Bool, nil
}
