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
	"sync"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testEssDBPath = "projects/p/instances/i/databases/ess"
	testXidDBPath = "projects/p/instances/i/databases/xid"
)

func newTestSpannerClient(t *testing.T, database string) *spanner.Client {
	t.Helper()
	conn, err := grpc.NewClient("localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create test grpc conn: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client, err := spanner.NewClient(context.Background(), database, option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("failed to create test spanner client: %v", err)
	}
	return client
}

func TestHealthServiceLazySpannerWrapper_EmptyDBPaths(t *testing.T) {
	ctx := context.Background()
	wrapper := NewHealthServiceLazySpannerWrapper("", "", nil)

	if _, err := wrapper.EssHealthClient(ctx); err == nil {
		t.Fatal("expected error for EssHealthClient with empty essDBPath, got nil")
	}
	if _, err := wrapper.ComponentsProvider(ctx); err == nil {
		t.Fatal("expected error for ComponentsProvider with empty essDBPath, got nil")
	}
	if _, err := wrapper.Storage(ctx); err == nil {
		t.Fatal("expected error for Storage with empty essDBPath, got nil")
	}
	if _, err := wrapper.XidHealthClient(ctx); err == nil {
		t.Fatal("expected error for XidHealthClient with empty xidDBPath, got nil")
	}
	if err := wrapper.Close(); err != nil {
		t.Fatalf("expected nil error on Close, got %v", err)
	}
}

func TestHealthServiceLazySpannerWrapper_LazyInitAndCaching(t *testing.T) {
	ctx := context.Background()
	var essCalls, xidCalls atomic.Int32

	factory := func(ctx context.Context, database string) (*spanner.Client, error) {
		switch database {
		case testEssDBPath:
			essCalls.Add(1)
		case testXidDBPath:
			xidCalls.Add(1)
		default:
			t.Fatalf("unexpected database path: %s", database)
		}
		return newTestSpannerClient(t, database), nil
	}

	wrapper := NewHealthServiceLazySpannerWrapper(testEssDBPath, testXidDBPath, factory)
	defer wrapper.Close()

	if essCalls.Load() != 0 || xidCalls.Load() != 0 {
		t.Fatalf("expected 0 factory calls on constructor, got ess=%d xid=%d", essCalls.Load(), xidCalls.Load())
	}

	// 1. Initialize ESS providers and verify they share a single Spanner client.
	essHealth1, err := wrapper.EssHealthClient(ctx)
	if err != nil || essHealth1 == nil {
		t.Fatalf("EssHealthClient failed: %v", err)
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
		t.Fatalf("expected ESS factory to be called once across all ESS providers, got %d", essCalls.Load())
	}
	if xidCalls.Load() != 0 {
		t.Fatalf("expected XID factory not to be called yet, got %d", xidCalls.Load())
	}

	// 2. Subsequent calls return cached instances without re-calling factory.
	essHealth2, err := wrapper.EssHealthClient(ctx)
	if err != nil || essHealth2 != essHealth1 {
		t.Fatalf("expected cached EssHealthClient, got %v (err: %v)", essHealth2, err)
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
		t.Fatalf("expected ESS factory call count to remain 1, got %d", essCalls.Load())
	}

	// 3. Initialize XID health client and verify caching.
	xidHealth1, err := wrapper.XidHealthClient(ctx)
	if err != nil || xidHealth1 == nil {
		t.Fatalf("XidHealthClient failed: %v", err)
	}
	xidHealth2, err := wrapper.XidHealthClient(ctx)
	if err != nil || xidHealth2 != xidHealth1 {
		t.Fatalf("expected cached XidHealthClient, got %v (err: %v)", xidHealth2, err)
	}
	if xidCalls.Load() != 1 {
		t.Fatalf("expected XID factory call count to be 1, got %d", xidCalls.Load())
	}

	// 4. Close resets state so subsequent calls re-initialize.
	if err := wrapper.Close(); err != nil {
		t.Fatalf("unexpected Close error: %v", err)
	}
	if _, err := wrapper.EssHealthClient(ctx); err != nil {
		t.Fatalf("EssHealthClient after Close failed: %v", err)
	}
	if _, err := wrapper.XidHealthClient(ctx); err != nil {
		t.Fatalf("XidHealthClient after Close failed: %v", err)
	}
	if essCalls.Load() != 2 || xidCalls.Load() != 2 {
		t.Fatalf("expected factory counts to be 2 after Close and re-init, got ess=%d xid=%d", essCalls.Load(), xidCalls.Load())
	}
}

func TestHealthServiceLazySpannerWrapper_InitialFailureThenLazyRecovery(t *testing.T) {
	ctx := context.Background()
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	factory := func(ctx context.Context, database string) (*spanner.Client, error) {
		if shouldFail.Load() {
			return nil, errors.New("simulated startup connection timeout")
		}
		return newTestSpannerClient(t, database), nil
	}

	wrapper := NewHealthServiceLazySpannerWrapper(testEssDBPath, testXidDBPath, factory)
	defer wrapper.Close()

	if _, err := wrapper.EssHealthClient(ctx); err == nil {
		t.Fatal("expected error on initial EssHealthClient, got nil")
	}
	if _, err := wrapper.ComponentsProvider(ctx); err == nil {
		t.Fatal("expected error on initial ComponentsProvider, got nil")
	}
	if _, err := wrapper.Storage(ctx); err == nil {
		t.Fatal("expected error on initial Storage, got nil")
	}
	if _, err := wrapper.XidHealthClient(ctx); err == nil {
		t.Fatal("expected error on initial XidHealthClient, got nil")
	}

	// Simulate Spanner becoming available.
	shouldFail.Store(false)

	if _, err := wrapper.EssHealthClient(ctx); err != nil {
		t.Fatalf("expected EssHealthClient recovery, got %v", err)
	}
	if _, err := wrapper.ComponentsProvider(ctx); err != nil {
		t.Fatalf("expected ComponentsProvider recovery, got %v", err)
	}
	if _, err := wrapper.Storage(ctx); err != nil {
		t.Fatalf("expected Storage recovery, got %v", err)
	}
	if _, err := wrapper.XidHealthClient(ctx); err != nil {
		t.Fatalf("expected XidHealthClient recovery, got %v", err)
	}
}

func TestHealthServiceLazySpannerWrapper_NilClientFromFactory(t *testing.T) {
	ctx := context.Background()
	factory := func(ctx context.Context, database string) (*spanner.Client, error) {
		return nil, nil
	}

	wrapper := NewHealthServiceLazySpannerWrapper(testEssDBPath, testXidDBPath, factory)
	if _, err := wrapper.EssHealthClient(ctx); err == nil {
		t.Fatal("expected error when factory returns nil ESS client, got nil")
	}
	if _, err := wrapper.XidHealthClient(ctx); err == nil {
		t.Fatal("expected error when factory returns nil XID client, got nil")
	}
}

func TestHealthServiceLazySpannerWrapper_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	var essCalls, xidCalls atomic.Int32

	factory := func(ctx context.Context, database string) (*spanner.Client, error) {
		switch database {
		case testEssDBPath:
			essCalls.Add(1)
		case testXidDBPath:
			xidCalls.Add(1)
		}
		return newTestSpannerClient(t, database), nil
	}

	wrapper := NewHealthServiceLazySpannerWrapper(testEssDBPath, testXidDBPath, factory)
	defer wrapper.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := wrapper.EssHealthClient(ctx); err != nil {
				t.Errorf("concurrent EssHealthClient error: %v", err)
			}
			if _, err := wrapper.ComponentsProvider(ctx); err != nil {
				t.Errorf("concurrent ComponentsProvider error: %v", err)
			}
			if _, err := wrapper.Storage(ctx); err != nil {
				t.Errorf("concurrent Storage error: %v", err)
			}
			if _, err := wrapper.XidHealthClient(ctx); err != nil {
				t.Errorf("concurrent XidHealthClient error: %v", err)
			}
		}()
	}
	wg.Wait()

	if essCalls.Load() != 1 {
		t.Fatalf("expected ESS factory to be called once under concurrency, got %d", essCalls.Load())
	}
	if xidCalls.Load() != 1 {
		t.Fatalf("expected XID factory to be called once under concurrency, got %d", xidCalls.Load())
	}
}
