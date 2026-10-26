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

package factory

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/option"
	"xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/storage/dbconfig"
	spannerstorage "xmanager_cloud/internal/storage/spanner"
	spannerversion "xmanager_cloud/internal/storage/spanner/migrations/version"
)

// openSpanner opens a Spanner client for db and wraps it in a Storage that owns the client.
func openSpanner(ctx context.Context, cfg dbconfig.Spanner, db Database) (*Storage, error) {
	database, expectedVersion := spannerDatabase(cfg, db)
	if database == "" {
		return nil, fmt.Errorf("no spanner database is configured for %s", db)
	}
	client, err := spanner.NewClientWithConfig(ctx, database, spannerClientConfig(), spannerClientOptions(cfg)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client for %s: %w", database, err)
	}
	st, err := spannerstorage.NewStorage(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create spanner storage for %s: %w", database, err)
	}
	return &Storage{
		Storage:         st,
		SpannerClient:   client,
		schemaVersion:   health.NewSpannerHealthClient(client),
		expectedVersion: expectedVersion,
		closeFn:         client.Close,
	}, nil
}

// spannerDatabase returns the database path configured for db and the Spanner schema version
// that this binary expects in it.
func spannerDatabase(cfg dbconfig.Spanner, db Database) (string, int64) {
	if db == XID {
		return cfg.XidDatabase, spannerversion.XidExpectedSchemaVersion()
	}
	return cfg.EssDatabase, spannerversion.EssExpectedSchemaVersion()
}

// spannerClientConfig tracks session handles and warns about inactive transactions.
func spannerClientConfig() spanner.ClientConfig {
	return spanner.ClientConfig{SessionPoolConfig: spanner.SessionPoolConfig{
		TrackSessionHandles: true,
		InactiveTransactionRemovalOptions: spanner.InactiveTransactionRemovalOptions{
			ActionOnInactiveTransaction: spanner.Warn,
		},
	}}
}

// spannerClientOptions bills Spanner requests to the configured quota project, if any.
func spannerClientOptions(cfg dbconfig.Spanner) []option.ClientOption {
	if cfg.QuotaProjectID == "" {
		return nil
	}
	return []option.ClientOption{option.WithQuotaProject(cfg.QuotaProjectID)}
}
