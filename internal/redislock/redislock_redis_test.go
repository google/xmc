// Copyright 2026 Google LLC
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

package redislock_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"xmanager_cloud/internal/redislock"
	"xmanager_cloud/internal/redistest"
)

var testRedisAddr string

func TestMain(m *testing.M) { os.Exit(redistest.RunMain(m, &testRedisAddr)) }

func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: testRedisAddr, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedisLock_AcquireExtendAndRelease(t *testing.T) {
	t.Parallel()
	ctx, client := context.Background(), newTestClient(t)
	const key = "xmc:test-lock:extend-release"
	lockerA, _ := redislock.New(client, 150*time.Millisecond)
	lockerB, _ := redislock.New(client, 150*time.Millisecond)

	leaseA, err := lockerA.Acquire(ctx, key)
	if err != nil {
		t.Fatalf("lockerA.Acquire() error = %v", err)
	}
	for i := range 4 {
		time.Sleep(60 * time.Millisecond)
		if err := leaseA.Extend(ctx); err != nil {
			t.Fatalf("leaseA.Extend() #%d error = %v", i+1, err)
		}
		if _, err := lockerB.Acquire(ctx, key); !errors.Is(err, redislock.ErrNotAcquired) {
			t.Fatalf("lockerB.Acquire() while leaseA held = %v, want ErrNotAcquired", err)
		}
	}
	if err := leaseA.Release(ctx); err != nil {
		t.Fatalf("leaseA.Release() error = %v", err)
	}
	leaseB, err := lockerB.Acquire(ctx, key)
	if err != nil {
		t.Fatalf("lockerB.Acquire() after Release error = %v", err)
	}
	if err := leaseB.Release(ctx); err != nil {
		t.Fatalf("leaseB.Release() error = %v", err)
	}
}

func TestRedisLock_StaleHolderCannotDeleteOrExtendNextOwner(t *testing.T) {
	t.Parallel()
	ctx, client := context.Background(), newTestClient(t)
	const key = "xmc:test-lock:token-safety"
	shortLocker, _ := redislock.New(client, 80*time.Millisecond)
	longLocker, _ := redislock.New(client, 5*time.Second)

	staleLease, err := shortLocker.Acquire(ctx, key)
	if err != nil {
		t.Fatalf("shortLocker.Acquire() error = %v", err)
	}
	var nextLease *redislock.Lease
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(15 * time.Millisecond) {
		nextLease, err = longLocker.Acquire(ctx, key)
		if err == nil {
			break
		}
		if !errors.Is(err, redislock.ErrNotAcquired) {
			t.Fatalf("longLocker.Acquire() unexpected error = %v", err)
		}
	}
	if nextLease == nil {
		t.Fatal("longLocker failed to acquire lock after short lease expiry")
	}
	defer func() { _ = nextLease.Release(ctx) }()

	if err := staleLease.Extend(ctx); !errors.Is(err, redislock.ErrLeaseLost) {
		t.Fatalf("staleLease.Extend() error = %v, want ErrLeaseLost", err)
	}
	if err := staleLease.Release(ctx); !errors.Is(err, redislock.ErrLeaseLost) {
		t.Fatalf("staleLease.Release() error = %v, want ErrLeaseLost", err)
	}
	if pttl, err := client.PTTL(ctx, key).Result(); err != nil || pttl < 2*time.Second {
		t.Errorf("Holder B PTTL = %v, err = %v; want >= 2s (untouched)", pttl, err)
	}
	if err := nextLease.Extend(ctx); err != nil {
		t.Errorf("nextLease.Extend() error = %v, want nil", err)
	}
	if err := nextLease.Release(ctx); err != nil {
		t.Errorf("nextLease.Release() error = %v, want nil", err)
	}
}

func TestRedisLock_MutualExclusionUnderConcurrency(t *testing.T) {
	t.Parallel()
	ctx, client := context.Background(), newTestClient(t)
	const key = "xmc:test-lock:mutual-exclusion"
	locker, _ := redislock.New(client, 2*time.Second)

	var inCS, acquisitions atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				lease, err := locker.Acquire(ctx, key)
				if errors.Is(err, redislock.ErrNotAcquired) {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				if err != nil {
					t.Errorf("Acquire() unexpected error = %v", err)
					return
				}
				if inCS.Add(1) != 1 {
					overlap.Store(true)
				}
				acquisitions.Add(1)
				time.Sleep(3 * time.Millisecond)
				if inCS.Add(-1) != 0 {
					overlap.Store(true)
				}
				if err := lease.Release(ctx); err != nil {
					t.Errorf("Release() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if overlap.Load() || acquisitions.Load() == 0 {
		t.Fatalf("overlap=%v acquisitions=%d, want false and >0", overlap.Load(), acquisitions.Load())
	}
}
