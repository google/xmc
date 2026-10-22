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

// Package factory opens the storage backend that the db-config settings select.
package factory

import (
	"context"
	"fmt"
	"sync"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/dbconfig"
)

// Database identifies one of the XMC databases.
type Database string

const (
	// ESS is the database of the Experiment State Server.
	ESS Database = "ess"
	// XID is the database of the XID service.
	XID Database = "xid"
)

// Store is an open XMC database: its storage, a readiness check and Close.
type Store struct {
	storage.Storage

	// SpannerClient is set only on the Spanner backend, for the components store.
	SpannerClient *spanner.Client

	schemaVersion   health.SchemaVersionClient
	expectedVersion int64
	closeFn         func()
	closeOnce       sync.Once
}

// CheckReady returns an error unless the database is reachable and migrated to the schema version
// that this binary expects.
func (s *Store) CheckReady(ctx context.Context) error {
	return health.CheckSchemaVersion(ctx, s.schemaVersion, s.expectedVersion)
}

// Close releases the database connections. Later calls do nothing, and the Store must not be used
// afterwards.
func (s *Store) Close() {
	if s.closeFn != nil {
		s.closeOnce.Do(s.closeFn)
	}
}

// Open connects to db through the backend that cfg selects.
func Open(ctx context.Context, cfg dbconfig.Config, db Database) (*Store, error) {
	if db != ESS && db != XID {
		return nil, fmt.Errorf("unknown database %q: must be %q or %q", db, ESS, XID)
	}
	switch cfg.Backend {
	case dbconfig.BackendSpanner:
		return openSpanner(ctx, cfg.Spanner, db)
	case dbconfig.BackendPostgres:
		return openPostgres(ctx, cfg.Postgres, db)
	default:
		return nil, fmt.Errorf("unsupported database backend %q", cfg.Backend)
	}
}
