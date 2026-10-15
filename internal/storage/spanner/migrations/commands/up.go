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

// Package commands provides functions for executing Spanner migration commands.
package commands

import (
	"context"
	"os"

	"cloud.google.com/go/spanner"
	"github.com/golang-migrate/migrate/v4"
	"google.golang.org/api/iterator"
	commonmigrations "xmanager_cloud/internal/storage/migrations"
)

// Up handles the migration up command.
func Up(ctx context.Context, migrator *migrate.Migrate, dataClient *spanner.Client, steps *int) error {
	return commonmigrations.Up(ctx, migrator, func(ctx context.Context) (bool, error) {
		return IsDatabaseEmpty(ctx, dataClient)
	}, steps, os.Stdout)
}

// IsDatabaseEmpty checks if the Spanner database contains any user-defined tables.
func IsDatabaseEmpty(ctx context.Context, dataClient *spanner.Client) (bool, error) {
	// Query the information schema to check for user-defined tables. We exclude the SchemaMigrations
	// table from the check because it can exist on empty db with nil version after running the "down" command.
	stmt := spanner.Statement{
		SQL: `SELECT 1 FROM INFORMATION_SCHEMA.TABLES
              WHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA', 'SPANNER_SYS')
              AND TABLE_NAME != 'SchemaMigrations' LIMIT 1`,
	}
	iter := dataClient.Single().Query(ctx, stmt)
	defer iter.Stop()
	_, err := iter.Next()
	if err == iterator.Done {
		// No rows were returned, so the database is empty of user tables.
		return true, nil
	}
	if err != nil {
		return false, err
	}

	// A row was found, so the database is not empty.
	return false, nil
}
