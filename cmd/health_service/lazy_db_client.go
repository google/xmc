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

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	health "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/storage/dbconfig"
	dbFactory "xmanager_cloud/internal/storage/factory"
	"xmanager_cloud/pkg/components/store/dbstore"
	"xmanager_cloud/pkg/components/summary"
)

var errDatabaseWrapperClosed = errors.New("database wrapper is closed")

// StorageOpener defines a factory function to open a database storage handle.
type StorageOpener func(ctx context.Context, cfg dbconfig.Config, db dbFactory.Database) (*dbFactory.Storage, error)

var (
	_ LazyDBCheckerProvider  = (*HealthServiceLazyDBWrapper)(nil)
	_ LazyComponentsProvider = (*HealthServiceLazyDBWrapper)(nil)
	_ LazyStorageProvider    = (*HealthServiceLazyDBWrapper)(nil)
)

// HealthServiceLazyDBWrapper provides thread-safe lazy initialization of database storage handles
// and database-backed providers (schema version health clients, custom components provider, and storage)
// for the health service.
type HealthServiceLazyDBWrapper struct {
	cfg    dbconfig.Config
	opener StorageOpener

	essMu              sync.Mutex
	essClosed          bool
	essStorage         *dbFactory.Storage
	componentsProvider summary.CustomComponentsInfoProvider

	xidMu      sync.Mutex
	xidClosed  bool
	xidStorage *dbFactory.Storage
}

// NewHealthServiceLazyDBWrapper creates a new HealthServiceLazyDBWrapper for the given database configuration.
func NewHealthServiceLazyDBWrapper(cfg dbconfig.Config, opener StorageOpener) *HealthServiceLazyDBWrapper {
	if opener == nil {
		opener = dbFactory.NewStorage
	}
	return &HealthServiceLazyDBWrapper{
		cfg:    cfg,
		opener: opener,
	}
}

// EssHealth returns the lazily-initialized SchemaVersionClient and expected schema version for the ESS database.
func (w *HealthServiceLazyDBWrapper) EssHealth(ctx context.Context) (health.SchemaVersionClient, int64, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	st, err := w.ensureEssStorageLocked(ctx)
	if err != nil {
		return nil, 0, err
	}
	client, expected := st.SchemaVersion()
	return client, expected, nil
}

// XidHealth returns the lazily-initialized SchemaVersionClient and expected schema version for the XID database.
func (w *HealthServiceLazyDBWrapper) XidHealth(ctx context.Context) (health.SchemaVersionClient, int64, error) {
	w.xidMu.Lock()
	defer w.xidMu.Unlock()

	st, err := w.ensureXidStorageLocked(ctx)
	if err != nil {
		return nil, 0, err
	}
	client, expected := st.SchemaVersion()
	return client, expected, nil
}

// ComponentsProvider returns the lazily-initialized CustomComponentsInfoProvider backed by the ESS database.
func (w *HealthServiceLazyDBWrapper) ComponentsProvider(ctx context.Context) (summary.CustomComponentsInfoProvider, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	if w.componentsProvider != nil {
		return w.componentsProvider, nil
	}

	st, err := w.ensureEssStorageLocked(ctx)
	if err != nil {
		return nil, err
	}

	compStore, err := dbstore.New(st)
	if err != nil {
		return nil, fmt.Errorf("failed to create component store: %w", err)
	}
	provider, err := summary.NewStoreComponentsProvider(compStore)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom components info provider: %w", err)
	}

	w.componentsProvider = provider
	return w.componentsProvider, nil
}

// Storage returns the lazily-initialized PauseSchedulingStorage for the ESS database.
func (w *HealthServiceLazyDBWrapper) Storage(ctx context.Context) (PauseSchedulingStorage, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	st, err := w.ensureEssStorageLocked(ctx)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// Close closes any initialized underlying database storage handles and marks the wrapper as closed.
func (w *HealthServiceLazyDBWrapper) Close() error {
	w.essMu.Lock()
	w.essClosed = true
	if w.essStorage != nil {
		w.essStorage.Close()
		w.essStorage = nil
	}
	w.componentsProvider = nil
	w.essMu.Unlock()

	w.xidMu.Lock()
	w.xidClosed = true
	if w.xidStorage != nil {
		w.xidStorage.Close()
		w.xidStorage = nil
	}
	w.xidMu.Unlock()

	return nil
}

func (w *HealthServiceLazyDBWrapper) ensureStorageLocked(ctx context.Context, db dbFactory.Database, cached *dbFactory.Storage, closed bool) (*dbFactory.Storage, error) {
	if closed {
		return nil, errDatabaseWrapperClosed
	}
	if cached != nil {
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st, err := w.opener(ctx, w.cfg, db)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s database storage: %w", db, err)
	}
	if st == nil || st.Storage == nil {
		return nil, fmt.Errorf("storage opener returned nil storage for %s", db)
	}
	return st, nil
}

func (w *HealthServiceLazyDBWrapper) ensureEssStorageLocked(ctx context.Context) (*dbFactory.Storage, error) {
	st, err := w.ensureStorageLocked(ctx, dbFactory.ESS, w.essStorage, w.essClosed)
	if err != nil {
		return nil, err
	}
	w.essStorage = st
	return w.essStorage, nil
}

func (w *HealthServiceLazyDBWrapper) ensureXidStorageLocked(ctx context.Context) (*dbFactory.Storage, error) {
	st, err := w.ensureStorageLocked(ctx, dbFactory.XID, w.xidStorage, w.xidClosed)
	if err != nil {
		return nil, err
	}
	w.xidStorage = st
	return w.xidStorage, nil
}
