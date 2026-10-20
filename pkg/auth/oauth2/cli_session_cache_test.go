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

package oauth2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// newTestCLISessionCache returns a cache in front of checker whose clock is *now.
func newTestCLISessionCache(checker CLISessionChecker, now *time.Time) *cliSessionCache {
	c := newCLISessionCache(checker)
	c.now = func() time.Time { return *now }
	return c
}

// cacheStep is one lookup through the cache, after advancing the clock by advance, optionally
// changing the store, and optionally caching a stale "active" answer as a racing lookup would.
type cacheStep struct {
	advance     time.Duration
	change      func(*fakeCLISessionStore)
	staleActive bool
	wantRevoked bool
	wantErr     bool
	// wantChecks is the total number of store lookups after this step.
	wantChecks int
}

func TestCLISessionCache(t *testing.T) {
	revoke := func(s *fakeCLISessionStore) { s.revoked["jti"] = time.Now() }
	fail := func(s *fakeCLISessionStore) { s.checkErr = errors.New("spanner unavailable") }
	heal := func(s *fakeCLISessionStore) { s.checkErr = nil }
	tests := []struct {
		name  string
		steps []cacheStep
	}{
		{name: "active session is cached for the TTL", steps: []cacheStep{
			{wantChecks: 1},
			{advance: cliSessionActiveTTL - time.Second, change: revoke, wantChecks: 1},
			{advance: time.Second, wantRevoked: true, wantChecks: 2},
		}},
		{name: "revoked session stays cached", steps: []cacheStep{
			{change: revoke, wantRevoked: true, wantChecks: 1},
			{advance: 30 * 24 * time.Hour, wantRevoked: true, wantChecks: 1},
		}},
		{name: "a stale active answer never undoes a revocation", steps: []cacheStep{
			{change: revoke, wantRevoked: true, wantChecks: 1},
			{staleActive: true, wantRevoked: true, wantChecks: 1},
		}},
		{name: "lookup errors are not cached", steps: []cacheStep{
			{change: fail, wantErr: true, wantChecks: 1},
			{change: heal, wantChecks: 2},
			{wantChecks: 2},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			store := newFakeCLISessionStore("jti")
			c := newTestCLISessionCache(store, &now)
			for i, step := range tt.steps {
				now = now.Add(step.advance)
				if step.change != nil {
					step.change(store)
				}
				if step.staleActive {
					c.store("jti", false)
				}
				revoked, err := c.IsRevoked(context.Background(), "jti")
				if gotErr := err != nil; gotErr != step.wantErr || revoked != step.wantRevoked {
					t.Errorf("step %d: IsRevoked() = %t, %v, want %t, error %t", i, revoked, err, step.wantRevoked, step.wantErr)
				}
				if got := store.checkCount(); got != step.wantChecks {
					t.Errorf("step %d: store lookups = %d, want %d", i, got, step.wantChecks)
				}
			}
		})
	}
}

func TestCLISessionCacheEviction(t *testing.T) {
	tests := []struct {
		name string
		// advance is how long after caching "a" and "b" the cache sees "c".
		advance time.Duration
		want    int
	}{
		{name: "all expired entries go", advance: cliSessionActiveTTL, want: 1},
		{name: "one live entry goes when none expired", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			c := newTestCLISessionCache(newFakeCLISessionStore("a", "b", "c"), &now)
			c.maxEntries = 2
			for _, jti := range []string{"a", "b"} {
				mustBeActive(t, c, jti)
			}
			now = now.Add(tt.advance)
			mustBeActive(t, c, "c")
			if _, ok := c.entries["c"]; !ok || len(c.entries) != tt.want {
				t.Errorf("cache holds %d entries (c cached: %t), want %d including c", len(c.entries), ok, tt.want)
			}
		})
	}
}

func mustBeActive(t *testing.T, c *cliSessionCache, jti string) {
	t.Helper()
	if revoked, err := c.IsRevoked(context.Background(), jti); err != nil || revoked {
		t.Fatalf("IsRevoked(%q) = %t, %v, want false, nil", jti, revoked, err)
	}
}

// gatedChecker reports every session active, but only once release is closed, and counts lookups.
type gatedChecker struct {
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (g *gatedChecker) IsRevoked(context.Context, string) (bool, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	<-g.release
	return false, nil
}

func TestCLISessionCacheSharesConcurrentLookups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checker := &gatedChecker{release: make(chan struct{})}
		now := time.Now()
		c := newTestCLISessionCache(checker, &now)
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				if revoked, err := c.IsRevoked(context.Background(), "jti"); err != nil || revoked {
					t.Errorf("IsRevoked() = %t, %v, want false, nil", revoked, err)
				}
			})
		}
		// Every caller is now blocked: one in the lookup, the others waiting for it.
		synctest.Wait()
		close(checker.release)
		wg.Wait()
		if checker.calls != 1 {
			t.Errorf("store lookups = %d, want 1", checker.calls)
		}
	})
}
