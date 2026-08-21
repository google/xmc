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

	health "xmanager_cloud/common/health"
)

type mockClosableSpannerHealthClient struct {
	version     int64
	dirty       bool
	err         error
	closeErr    error
	closeCalled bool
}

func (m *mockClosableSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	return m.version, m.dirty, m.err
}

func (m *mockClosableSpannerHealthClient) Close() error {
	m.closeCalled = true
	return m.closeErr
}

func TestLazySpannerHealthClient_EmptyDbPath(t *testing.T) {
	client := NewLazySpannerHealthClient("", nil)

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("expected nil error on empty dbPath, got %v", err)
	}

	_, _, err := client.QuerySchemaVersion(context.Background())
	if err == nil {
		t.Fatal("expected error on empty dbPath query, got nil")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("expected nil error on Close, got %v", err)
	}
}

func TestLazySpannerHealthClient_EagerSuccess(t *testing.T) {
	mockCl := &mockClosableSpannerHealthClient{version: 42, dirty: false}
	factoryCalled := false
	factory := func(ctx context.Context, database string) (health.SpannerHealthClient, error) {
		factoryCalled = true
		return mockCl, nil
	}

	client := NewLazySpannerHealthClient("projects/p/instances/i/databases/d", factory)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("expected nil error on Initialize, got %v", err)
	}

	if !factoryCalled {
		t.Fatal("expected factory to be called during Initialize")
	}

	version, dirty, err := client.QuerySchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on QuerySchemaVersion: %v", err)
	}
	if version != 42 || dirty {
		t.Fatalf("expected version 42 dirty false, got %d dirty %v", version, dirty)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("unexpected error on Close: %v", err)
	}
	if !mockCl.closeCalled {
		t.Fatal("expected underlying client to be closed")
	}
}

func TestLazySpannerHealthClient_InitialFailureThenLazyRecovery(t *testing.T) {
	var callCount atomic.Int32
	mockCl := &mockClosableSpannerHealthClient{version: 99, dirty: false}

	factory := func(ctx context.Context, database string) (health.SpannerHealthClient, error) {
		count := callCount.Add(1)
		if count == 1 {
			return nil, errors.New("simulated startup connection timeout")
		}
		return mockCl, nil
	}

	client := NewLazySpannerHealthClient("projects/p/instances/i/databases/d", factory)

	err := client.Initialize(context.Background())
	if err == nil {
		t.Fatal("expected error on initial Initialize, got nil")
	}

	version, dirty, err := client.QuerySchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("expected successful recovery on QuerySchemaVersion, got %v", err)
	}
	if version != 99 || dirty {
		t.Fatalf("expected version 99 dirty false, got %d dirty %v", version, dirty)
	}

	if callCount.Load() != 2 {
		t.Fatalf("expected factory to be called exactly 2 times, got %d", callCount.Load())
	}
}

func TestLazySpannerHealthClient_QueryErrorResetsClient(t *testing.T) {
	var callCount atomic.Int32
	mockFailing := &mockClosableSpannerHealthClient{err: errors.New("session closed")}
	mockRecovered := &mockClosableSpannerHealthClient{version: 100, dirty: false}

	factory := func(ctx context.Context, database string) (health.SpannerHealthClient, error) {
		count := callCount.Add(1)
		if count == 1 {
			return mockFailing, nil
		}
		return mockRecovered, nil
	}

	client := NewLazySpannerHealthClient("projects/p/instances/i/databases/d", factory)

	_, _, err := client.QuerySchemaVersion(context.Background())
	if err == nil {
		t.Fatal("expected error from failing query, got nil")
	}

	version, dirty, err := client.QuerySchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("expected successful recovery after query failure, got %v", err)
	}
	if version != 100 || dirty {
		t.Fatalf("expected version 100, got %d (dirty: %v)", version, dirty)
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected factory called 2 times, got %d", callCount.Load())
	}
}

func TestLazySpannerHealthClient_ConcurrentAccess(t *testing.T) {
	mockCl := &mockClosableSpannerHealthClient{version: 7, dirty: false}
	factory := func(ctx context.Context, database string) (health.SpannerHealthClient, error) {
		return mockCl, nil
	}

	client := NewLazySpannerHealthClient("projects/p/instances/i/databases/d", factory)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, d, err := client.QuerySchemaVersion(context.Background())
			if err != nil || v != 7 || d {
				t.Errorf("unexpected concurrent query result: v=%d d=%v err=%v", v, d, err)
			}
		}()
	}
	wg.Wait()

	if err := client.Close(); err != nil {
		t.Fatalf("unexpected Close error: %v", err)
	}
}

func TestLazySpannerHealthClient_CloseError(t *testing.T) {
	expectedErr := errors.New("close failed")
	mockCl := &mockClosableSpannerHealthClient{version: 1, closeErr: expectedErr}
	factory := func(ctx context.Context, database string) (health.SpannerHealthClient, error) {
		return mockCl, nil
	}

	client := NewLazySpannerHealthClient("projects/p/instances/i/databases/d", factory)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("unexpected Initialize error: %v", err)
	}

	if err := client.Close(); !errors.Is(err, expectedErr) {
		t.Fatalf("expected close error %v, got %v", expectedErr, err)
	}
}
