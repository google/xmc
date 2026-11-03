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

// Storage is an open XMC database: its storage, a readiness check and Close.
type Storage struct {
	storage.Storage

	schemaVersion   health.SchemaVersionClient
	expectedVersion int64
	closeFn         func()
	closeOnce       sync.Once
}

// New wraps st with its schema-version client, expected schema version and close callback.
func New(st storage.Storage, schemaVersion health.SchemaVersionClient, expectedVersion int64, closeFn func()) *Storage {
	return &Storage{
		Storage:         st,
		schemaVersion:   schemaVersion,
		expectedVersion: expectedVersion,
		closeFn:         closeFn,
	}
}

// SchemaVersion returns the database's schema-version client and the schema version that this
// binary expects.
func (s *Storage) SchemaVersion() (health.SchemaVersionClient, int64) {
	if s == nil {
		return nil, 0
	}
	return s.schemaVersion, s.expectedVersion
}

// CheckReady returns an error unless the database is reachable and migrated to the schema version
// that this binary expects.
func (s *Storage) CheckReady(ctx context.Context) error {
	return health.CheckSchemaVersion(ctx, s.schemaVersion, s.expectedVersion)
}

// Close releases the database connections. Later calls do nothing, and the Storage must not be used
// afterwards.
func (s *Storage) Close() {
	if s != nil && s.closeFn != nil {
		s.closeOnce.Do(s.closeFn)
	}
}

// NewStorage connects to db through the backend that cfg selects.
func NewStorage(ctx context.Context, cfg dbconfig.Config, db Database) (*Storage, error) {
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
