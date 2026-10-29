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
	"sync/atomic"
	"testing"

	"xmanager_cloud/internal/broadcast"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/dbconfig"
	"xmanager_cloud/internal/storage/factory"
	storagetest "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

const (
	testEssDBPath = "projects/p/instances/i/databases/ess"
	testXidDBPath = "projects/p/instances/i/databases/xid"
)

func testSpannerDBConfig() dbconfig.Config {
	return dbconfig.Config{
		Backend: dbconfig.BackendSpanner,
		Spanner: dbconfig.Spanner{
			EssDatabase: testEssDBPath,
			XidDatabase: testXidDBPath,
		},
	}
}

func newTestFactoryStorage(db factory.Database, closeFn func()) *factory.Storage {
	expectedVersion := int64(21)
	if db == factory.XID {
		expectedVersion = 1
	}
	return factory.New(
		&storage.MockStorage{
			PauseStateRepository: storagetest.NewFakePauseStateRepository(),
		},
		&mockSpannerHealthClient{version: expectedVersion},
		expectedVersion,
		closeFn,
	)
}

func TestHealthServiceLazyDBWrapper_EmptyDBConfig(t *testing.T) {
	ctx := context.Background()
	for _, cfg := range []dbconfig.Config{
		{Backend: dbconfig.BackendSpanner},
		{Backend: dbconfig.BackendPostgres},
	} {
		t.Run(string(cfg.Backend), func(t *testing.T) {
			wrapper := NewHealthServiceLazyDBWrapper(cfg, nil)

			if _, _, err := wrapper.EssHealth(ctx); err == nil {
				t.Fatal("expected error for EssHealth with unconfigured database, got nil")
			}
			if _, err := wrapper.ComponentsProvider(ctx); err == nil {
				t.Fatal("expected error for ComponentsProvider with unconfigured database, got nil")
			}
			if _, err := wrapper.Storage(ctx); err == nil {
				t.Fatal("expected error for Storage with unconfigured database, got nil")
			}
			if _, _, err := wrapper.XidHealth(ctx); err == nil {
				t.Fatal("expected error for XidHealth with unconfigured database, got nil")
			}
			if err := wrapper.Close(); err != nil {
				t.Fatalf("expected nil error on Close, got %v", err)
			}
		})
	}
}

func TestHealthServiceLazyDBWrapper_LazyInitAndCaching(t *testing.T) {
	ctx := context.Background()
	var essCalls, xidCalls, essCloses, xidCloses atomic.Int32

	opener := func(_ context.Context, _ dbconfig.Config, db factory.Database) (*factory.Storage, error) {
		switch db {
		case factory.ESS:
			essCalls.Add(1)
			return newTestFactoryStorage(db, func() {
				essCloses.Add(1)
			}), nil
		case factory.XID:
			xidCalls.Add(1)
			return newTestFactoryStorage(db, func() {
				xidCloses.Add(1)
			}), nil
		default:
			return nil, fmt.Errorf("unexpected database: %s", db)
		}
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	defer wrapper.Close()

	if essCalls.Load() != 0 || xidCalls.Load() != 0 {
		t.Fatalf("expected 0 opener calls on constructor, got ess=%d xid=%d", essCalls.Load(), xidCalls.Load())
	}

	// 1. Initialize ESS providers and verify they share a single storage handle.
	essHealth1, essExpected1, err := wrapper.EssHealth(ctx)
	if err != nil || essHealth1 == nil || essExpected1 != 21 {
		t.Fatalf("EssHealth failed: client=%v expected=%d err=%v", essHealth1, essExpected1, err)
	}
	compProvider1, err := wrapper.ComponentsProvider(ctx)
	if err != nil || compProvider1 == nil {
		t.Fatalf("ComponentsProvider failed: %v", err)
	}
	st1, err := wrapper.Storage(ctx)
	if err != nil || st1 == nil {
		t.Fatalf("Storage failed: %v", err)
	}
	if essCalls.Load() != 1 {
		t.Fatalf("expected ESS opener to be called once across all ESS providers, got %d", essCalls.Load())
	}
	if xidCalls.Load() != 0 {
		t.Fatalf("expected XID opener not to be called yet, got %d", xidCalls.Load())
	}

	// Verify PauseScheduler storage wiring works end-to-end through the lazy wrapper.
	scheduler := NewPauseScheduler(PauseSchedulerParams{
		LazyStorage:        wrapper,
		BroadcastPublisher: broadcast.NewFakeClient(""),
		TaskQueuePublisher: redisclient.NewFakeClient(),
	})
	if err := scheduler.Pause(ctx); err != nil {
		t.Fatalf("scheduler.Pause() via lazy wrapper failed: %v", err)
	}
	paused, err := scheduler.IsPaused(ctx)
	if err != nil || !paused {
		t.Fatalf("scheduler.IsPaused() = (%v, %v), want (true, nil)", paused, err)
	}

	// 2. Subsequent calls return cached instances without re-calling opener.
	essHealth2, essExpected2, err := wrapper.EssHealth(ctx)
	if err != nil || essHealth2 != essHealth1 || essExpected2 != essExpected1 {
		t.Fatalf("expected cached EssHealth, got %v/%d (err: %v)", essHealth2, essExpected2, err)
	}
	compProvider2, err := wrapper.ComponentsProvider(ctx)
	if err != nil || compProvider2 != compProvider1 {
		t.Fatalf("expected cached ComponentsProvider, got %v (err: %v)", compProvider2, err)
	}
	st2, err := wrapper.Storage(ctx)
	if err != nil || st2 != st1 {
		t.Fatalf("expected cached Storage, got %v (err: %v)", st2, err)
	}
	if essCalls.Load() != 1 {
		t.Fatalf("expected ESS opener call count to remain 1, got %d", essCalls.Load())
	}

	// 3. Initialize XID health client and verify caching.
	xidHealth1, xidExpected1, err := wrapper.XidHealth(ctx)
	if err != nil || xidHealth1 == nil || xidExpected1 != 1 {
		t.Fatalf("XidHealth failed: client=%v expected=%d err=%v", xidHealth1, xidExpected1, err)
	}
	xidHealth2, xidExpected2, err := wrapper.XidHealth(ctx)
	if err != nil || xidHealth2 != xidHealth1 || xidExpected2 != xidExpected1 {
		t.Fatalf("expected cached XidHealth, got %v/%d (err: %v)", xidHealth2, xidExpected2, err)
	}
	if xidCalls.Load() != 1 {
		t.Fatalf("expected XID opener call count to be 1, got %d", xidCalls.Load())
	}

	// 4. Close closes underlying storage handles and rejects subsequent calls without re-initializing.
	if err := wrapper.Close(); err != nil {
		t.Fatalf("unexpected Close error: %v", err)
	}
	if essCloses.Load() != 1 || xidCloses.Load() != 1 {
		t.Fatalf("expected 1 Close call per handle, got ess=%d xid=%d", essCloses.Load(), xidCloses.Load())
	}
	if _, _, err := wrapper.EssHealth(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Fatalf("expected errDatabaseWrapperClosed after Close for EssHealth, got %v", err)
	}
	if _, err := wrapper.ComponentsProvider(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Fatalf("expected errDatabaseWrapperClosed after Close for ComponentsProvider, got %v", err)
	}
	if _, err := wrapper.Storage(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Fatalf("expected errDatabaseWrapperClosed after Close for Storage, got %v", err)
	}
	if _, _, err := wrapper.XidHealth(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Fatalf("expected errDatabaseWrapperClosed after Close for XidHealth, got %v", err)
	}
	if essCalls.Load() != 1 || xidCalls.Load() != 1 {
		t.Fatalf("expected opener counts to remain 1 after Close, got ess=%d xid=%d", essCalls.Load(), xidCalls.Load())
	}
}

func TestHealthServiceLazyDBWrapper_InitialFailureThenLazyRecovery(t *testing.T) {
	ctx := context.Background()
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	opener := func(_ context.Context, _ dbconfig.Config, db factory.Database) (*factory.Storage, error) {
		if shouldFail.Load() {
			return nil, errors.New("simulated startup connection timeout")
		}
		return newTestFactoryStorage(db, nil), nil
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	defer wrapper.Close()

	if _, _, err := wrapper.EssHealth(ctx); err == nil {
		t.Fatal("expected error on initial EssHealth, got nil")
	}
	if _, err := wrapper.ComponentsProvider(ctx); err == nil {
		t.Fatal("expected error on initial ComponentsProvider, got nil")
	}
	if _, err := wrapper.Storage(ctx); err == nil {
		t.Fatal("expected error on initial Storage, got nil")
	}
	if _, _, err := wrapper.XidHealth(ctx); err == nil {
		t.Fatal("expected error on initial XidHealth, got nil")
	}

	// Simulate database becoming available.
	shouldFail.Store(false)

	if _, _, err := wrapper.EssHealth(ctx); err != nil {
		t.Fatalf("expected EssHealth recovery, got %v", err)
	}
	if _, err := wrapper.ComponentsProvider(ctx); err != nil {
		t.Fatalf("expected ComponentsProvider recovery, got %v", err)
	}
	if _, err := wrapper.Storage(ctx); err != nil {
		t.Fatalf("expected Storage recovery, got %v", err)
	}
	if _, _, err := wrapper.XidHealth(ctx); err != nil {
		t.Fatalf("expected XidHealth recovery, got %v", err)
	}
}

func TestHealthServiceLazyDBWrapper_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var openerCalled atomic.Bool
	opener := func(_ context.Context, _ dbconfig.Config, db factory.Database) (*factory.Storage, error) {
		openerCalled.Store(true)
		return newTestFactoryStorage(db, nil), nil
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	if _, _, err := wrapper.EssHealth(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled for EssHealth, got %v", err)
	}
	if _, err := wrapper.ComponentsProvider(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled for ComponentsProvider, got %v", err)
	}
	if _, err := wrapper.Storage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled for Storage, got %v", err)
	}
	if _, _, err := wrapper.XidHealth(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled for XidHealth, got %v", err)
	}
	if openerCalled.Load() {
		t.Fatal("expected opener not to be called when context is already canceled")
	}
}

func TestHealthServiceLazyDBWrapper_NilStorageFromOpener(t *testing.T) {
	ctx := context.Background()
	opener := func(_ context.Context, _ dbconfig.Config, _ factory.Database) (*factory.Storage, error) {
		return nil, nil
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	if _, _, err := wrapper.EssHealth(ctx); err == nil {
		t.Fatal("expected error when opener returns nil ESS storage, got nil")
	}
	if _, _, err := wrapper.XidHealth(ctx); err == nil {
		t.Fatal("expected error when opener returns nil XID storage, got nil")
	}
}

func TestHealthServiceLazyDBWrapper_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	var essCalls, xidCalls atomic.Int32

	opener := func(_ context.Context, _ dbconfig.Config, db factory.Database) (*factory.Storage, error) {
		switch db {
		case factory.ESS:
			essCalls.Add(1)
		case factory.XID:
			xidCalls.Add(1)
		}
		return newTestFactoryStorage(db, nil), nil
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	defer wrapper.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := wrapper.EssHealth(ctx); err != nil {
				t.Errorf("concurrent EssHealth error: %v", err)
			}
			if _, err := wrapper.ComponentsProvider(ctx); err != nil {
				t.Errorf("concurrent ComponentsProvider error: %v", err)
			}
			if _, err := wrapper.Storage(ctx); err != nil {
				t.Errorf("concurrent Storage error: %v", err)
			}
			if _, _, err := wrapper.XidHealth(ctx); err != nil {
				t.Errorf("concurrent XidHealth error: %v", err)
			}
		}()
	}
	wg.Wait()

	if essCalls.Load() != 1 {
		t.Fatalf("expected ESS opener to be called once under concurrency, got %d", essCalls.Load())
	}
	if xidCalls.Load() != 1 {
		t.Fatalf("expected XID opener to be called once under concurrency, got %d", xidCalls.Load())
	}
}

func TestHealthServiceLazyDBWrapper_CloseBeforeInit(t *testing.T) {
	ctx := context.Background()
	var openerCalled atomic.Bool
	opener := func(_ context.Context, _ dbconfig.Config, db factory.Database) (*factory.Storage, error) {
		openerCalled.Store(true)
		return newTestFactoryStorage(db, nil), nil
	}

	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), opener)
	if err := wrapper.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, _, err := wrapper.EssHealth(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Errorf("expected errDatabaseWrapperClosed for EssHealth, got %v", err)
	}
	if _, err := wrapper.ComponentsProvider(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Errorf("expected errDatabaseWrapperClosed for ComponentsProvider, got %v", err)
	}
	if _, err := wrapper.Storage(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Errorf("expected errDatabaseWrapperClosed for Storage, got %v", err)
	}
	if _, _, err := wrapper.XidHealth(ctx); !errors.Is(err, errDatabaseWrapperClosed) {
		t.Errorf("expected errDatabaseWrapperClosed for XidHealth, got %v", err)
	}
	if openerCalled.Load() {
		t.Errorf("expected opener not to be called when wrapper is closed before init")
	}
}

func TestHealthServiceLazyDBWrapper_CloseIdempotent(t *testing.T) {
	wrapper := NewHealthServiceLazyDBWrapper(testSpannerDBConfig(), nil)
	for i := 0; i < 3; i++ {
		if err := wrapper.Close(); err != nil {
			t.Errorf("call %d: unexpected error on Close: %v", i+1, err)
		}
	}
}
