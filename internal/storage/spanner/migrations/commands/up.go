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
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/golang-migrate/migrate/v4"
	"google.golang.org/api/iterator"
)

// Up handles the migration up command.
func Up(ctx context.Context, migrator *migrate.Migrate, dataClient *spanner.Client, steps *int) error {
	// Check if we need to adopt the database from before the migration system was introduced.
	migrationsInitialized, err := verifySchemaMigrationsInitialized(ctx, migrator)
	if err != nil {
		return err
	}

	if !migrationsInitialized {
		isEmpty, err := isDatabaseEmpty(ctx, dataClient)
		if err != nil {
			return fmt.Errorf("failed to check if database is empty: %w", err)
		}
		if !isEmpty {
			return fmt.Errorf("schema migrations table is not yet initialized in a non-empty database. " +
				"Please verify your schema and run the 'force' command to set the correct migration version")
		}
		fmt.Println("Database is empty, proceeding with initial migration up.")
	}

	// if argument was set apply specified number of steps, otherwise apply all migrations.
	if steps != nil {
		if *steps < 1 {
			return fmt.Errorf("step argument for 'up' command must be greater than 0")
		}
		err = migrator.Steps(*steps)
	} else {
		err = migrator.Up()
	}
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	if err == migrate.ErrNoChange {
		fmt.Println("No migrations to apply. Schema is already up to date.")
		return nil
	}

	if steps != nil {
		fmt.Printf("Migration up %d steps completed successfully.\n", *steps)
	} else {
		fmt.Println("Migration up completed successfully.")
	}
	return nil
}

// verifySchemaMigrationsInitialized verifies that the schema migrations are initialized.
func verifySchemaMigrationsInitialized(ctx context.Context, migrator *migrate.Migrate) (bool, error) {
	_, _, err := migrator.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return false, fmt.Errorf("failed to get migration version: %w", err)
	}
	if err == migrate.ErrNilVersion {
		return false, nil
	}

	return true, nil
}

// isDatabaseEmpty checks if the Spanner database contains any user-defined tables.
func isDatabaseEmpty(ctx context.Context, dataClient *spanner.Client) (bool, error) {
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
