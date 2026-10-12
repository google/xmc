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

	health "github.com/google/xmc/internal/common/health"
	spannerstorage "github.com/google/xmc/internal/storage/spanner"
	"github.com/google/xmc/pkg/components/store"
	"github.com/google/xmc/pkg/components/summary"

	"cloud.google.com/go/spanner"
)

var errSpannerWrapperClosed = errors.New("spanner wrapper is closed")

// SpannerClientFactory defines a factory function to instantiate a Spanner client.
type SpannerClientFactory func(ctx context.Context, database string) (*spanner.Client, error)

// DefaultSpannerClientFactory creates a real Spanner client using spanner.NewClient.
// It uses context.Background() so short-lived request timeouts do not cancel the cached client's session pool.
func DefaultSpannerClientFactory(_ context.Context, database string) (*spanner.Client, error) {
	return spanner.NewClient(context.Background(), database)
}

var (
	_ LazySpannerCheckerProvider = (*HealthServiceLazySpannerWrapper)(nil)
	_ LazyComponentsProvider     = (*HealthServiceLazySpannerWrapper)(nil)
	_ LazyStorageProvider        = (*HealthServiceLazySpannerWrapper)(nil)
)

// HealthServiceLazySpannerWrapper provides thread-safe lazy initialization of Spanner clients
// and Spanner-backed providers (health clients, custom components provider, and storage)
// for the health service.
type HealthServiceLazySpannerWrapper struct {
	factory SpannerClientFactory

	essMu              sync.Mutex
	essClosed          bool
	essDBPath          string
	essClient          *spanner.Client
	essHealthClient    health.SpannerHealthClient
	componentsProvider summary.CustomComponentsInfoProvider
	storage            PauseSchedulingStorage

	xidMu           sync.Mutex
	xidClosed       bool
	xidDBPath       string
	xidClient       *spanner.Client
	xidHealthClient health.SpannerHealthClient
}

// NewHealthServiceLazySpannerWrapper creates a new HealthServiceLazySpannerWrapper for the given ESS and XID database paths.
func NewHealthServiceLazySpannerWrapper(essDBPath, xidDBPath string, factory SpannerClientFactory) *HealthServiceLazySpannerWrapper {
	if factory == nil {
		factory = DefaultSpannerClientFactory
	}
	return &HealthServiceLazySpannerWrapper{
		factory:   factory,
		essDBPath: essDBPath,
		xidDBPath: xidDBPath,
	}
}

// EssHealthClient returns the lazily-initialized SpannerHealthClient for the ESS database.
func (w *HealthServiceLazySpannerWrapper) EssHealthClient(ctx context.Context) (health.SpannerHealthClient, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	if w.essHealthClient != nil {
		return w.essHealthClient, nil
	}

	client, err := w.ensureEssClientLocked(ctx)
	if err != nil {
		return nil, err
	}

	w.essHealthClient = health.NewSpannerHealthClient(client)
	return w.essHealthClient, nil
}

// XidHealthClient returns the lazily-initialized SpannerHealthClient for the XID database.
func (w *HealthServiceLazySpannerWrapper) XidHealthClient(ctx context.Context) (health.SpannerHealthClient, error) {
	w.xidMu.Lock()
	defer w.xidMu.Unlock()

	if w.xidHealthClient != nil {
		return w.xidHealthClient, nil
	}

	client, err := w.ensureXidClientLocked(ctx)
	if err != nil {
		return nil, err
	}

	w.xidHealthClient = health.NewSpannerHealthClient(client)
	return w.xidHealthClient, nil
}

// ComponentsProvider returns the lazily-initialized CustomComponentsInfoProvider backed by the ESS database.
func (w *HealthServiceLazySpannerWrapper) ComponentsProvider(ctx context.Context) (summary.CustomComponentsInfoProvider, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	if w.componentsProvider != nil {
		return w.componentsProvider, nil
	}

	client, err := w.ensureEssClientLocked(ctx)
	if err != nil {
		return nil, err
	}

	compStore := store.NewSpannerStore(client)
	provider, err := summary.NewStoreComponentsProvider(compStore)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom components info provider: %w", err)
	}

	w.componentsProvider = provider
	return w.componentsProvider, nil
}

// Storage returns the lazily-initialized Spanner-backed PauseSchedulingStorage for the ESS database.
func (w *HealthServiceLazySpannerWrapper) Storage(ctx context.Context) (PauseSchedulingStorage, error) {
	w.essMu.Lock()
	defer w.essMu.Unlock()

	if w.storage != nil {
		return w.storage, nil
	}

	client, err := w.ensureEssClientLocked(ctx)
	if err != nil {
		return nil, err
	}

	st, err := spannerstorage.NewStorage(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner storage: %w", err)
	}

	w.storage = st
	return w.storage, nil
}

// Close closes any initialized underlying Spanner clients and marks the wrapper as closed.
func (w *HealthServiceLazySpannerWrapper) Close() error {
	w.essMu.Lock()
	w.essClosed = true
	if w.essClient != nil {
		w.essClient.Close()
		w.essClient = nil
	}
	w.essHealthClient = nil
	w.componentsProvider = nil
	w.storage = nil
	w.essMu.Unlock()

	w.xidMu.Lock()
	w.xidClosed = true
	if w.xidClient != nil {
		w.xidClient.Close()
		w.xidClient = nil
	}
	w.xidHealthClient = nil
	w.xidMu.Unlock()

	return nil
}

func (w *HealthServiceLazySpannerWrapper) ensureClientLocked(ctx context.Context, dbPath, dbName string, cachedClient *spanner.Client, closed bool) (*spanner.Client, error) {
	if closed {
		return nil, errSpannerWrapperClosed
	}
	if dbPath == "" {
		return nil, fmt.Errorf("%s spanner database path is not configured", dbName)
	}
	if cachedClient != nil {
		return cachedClient, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := w.factory(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create spanner client for %s: %w", dbPath, err)
	}
	if client == nil {
		return nil, fmt.Errorf("spanner client factory returned nil client for %s", dbPath)
	}
	return client, nil
}

func (w *HealthServiceLazySpannerWrapper) ensureEssClientLocked(ctx context.Context) (*spanner.Client, error) {
	client, err := w.ensureClientLocked(ctx, w.essDBPath, "ess", w.essClient, w.essClosed)
	if err != nil {
		return nil, err
	}
	w.essClient = client
	return w.essClient, nil
}

func (w *HealthServiceLazySpannerWrapper) ensureXidClientLocked(ctx context.Context) (*spanner.Client, error) {
	client, err := w.ensureClientLocked(ctx, w.xidDBPath, "xid", w.xidClient, w.xidClosed)
	if err != nil {
		return nil, err
	}
	w.xidClient = client
	return w.xidClient, nil
}
