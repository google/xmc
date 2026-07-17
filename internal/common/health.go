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
// Package health contains shared health check functions used by other services in the xmanager_cloud project.
package health

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"k8s.io/client-go/kubernetes"
	"xmanager_cloud/logging/logging"
)

// ErrNilClient is returned when a client is nil.
var ErrNilClient = errors.New("client is nil")

// CheckSpanner validates that a Spanner client is connected and ready using a simple query.
func CheckSpanner(ctx context.Context, sc *spanner.Client) error {
	if sc == nil {
		return fmt.Errorf("spanner: %w", ErrNilClient)
	}

	iter := sc.Single().Query(ctx, spanner.Statement{SQL: "SELECT 1"})
	defer iter.Stop()
	_, err := iter.Next()
	if err != nil && err != iterator.Done {
		return fmt.Errorf("spanner reachability: %w", err)
	}
	logging.Debug(ctx, "Spanner health check completed successfully")
	return nil
}

// CheckSpannerWithVersion validates that Spanner is connected, ready, and has the expected schema version.
func CheckSpannerWithVersion(ctx context.Context, sc *spanner.Client, expectedVersion int64) error {
	return CheckSchemaMigrationVersion(ctx, sc, expectedVersion)
}

// CheckSchemaMigrationVersion queries the SchemaMigrations table and checks if the version is at least the expected version and not dirty.
func CheckSchemaMigrationVersion(ctx context.Context, sc *spanner.Client, expectedVersion int64) error {
	if sc == nil {
		return fmt.Errorf("spanner: %w", ErrNilClient)
	}

	stmt := spanner.Statement{
		SQL: "SELECT Version, Dirty FROM SchemaMigrations LIMIT 1",
	}
	iter := sc.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err == iterator.Done {
		return fmt.Errorf("no migration records found in SchemaMigrations table")
	}
	if err != nil {
		return fmt.Errorf("failed to query SchemaMigrations: %w", err)
	}

	var version int64
	var dirty bool
	if err := row.Columns(&version, &dirty); err != nil {
		return fmt.Errorf("failed to parse SchemaMigrations columns: %w", err)
	}

	if dirty {
		return fmt.Errorf("schema migration is in a dirty state at version %d", version)
	}

	if version < expectedVersion {
		return fmt.Errorf("schema version %d is older than expected version %d", version, expectedVersion)
	}

	logging.Debug(ctx, "Schema migration version check completed successfully (version: %d)", version)
	return nil
}

// CheckKubernetes validates that a Kubernetes clientset is connected and ready.
func CheckKubernetes(clientset kubernetes.Interface) error {
	if clientset == nil || clientset.Discovery() == nil {
		return fmt.Errorf("kubernetes: %w", ErrNilClient)
	}
	_, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("kubernetes reachability: %w", err)
	}
	return nil
}
