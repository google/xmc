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
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// cliSessionActiveTTL bounds how long an active session is cached, and so how long a revocation
	// takes to reach every replica of every service.
	cliSessionActiveTTL = 60 * time.Second
	// cliSessionCacheSize bounds the number of cached sessions.
	cliSessionCacheSize = 10000
	// cliSessionLookupTimeout bounds a session lookup shared by concurrent requests.
	cliSessionLookupTimeout = 10 * time.Second
)

// cliSessionCache is a CLISessionChecker that caches the answers of another one, so most requests
// do not reach the session store. Active sessions are cached for cliSessionActiveTTL. Revocation is
// permanent, so revoked sessions stay cached until evicted and are never downgraded to active.
// Lookup errors are not cached. Concurrent misses for one session share a single lookup.
type cliSessionCache struct {
	checker    CLISessionChecker
	now        func() time.Time
	maxEntries int
	lookups    singleflight.Group

	mu      sync.Mutex
	entries map[string]cliSessionCacheEntry
}

type cliSessionCacheEntry struct {
	revoked bool
	// expiresAt is when an active entry must be looked up again. Revoked entries never expire.
	expiresAt time.Time
}

var _ CLISessionChecker = (*cliSessionCache)(nil)

// newCLISessionCache returns a cache in front of checker, which must not be nil.
func newCLISessionCache(checker CLISessionChecker) *cliSessionCache {
	return &cliSessionCache{
		checker:    checker,
		now:        time.Now,
		maxEntries: cliSessionCacheSize,
		entries:    make(map[string]cliSessionCacheEntry),
	}
}

// IsRevoked returns the cached answer for jti, or asks the underlying checker and caches its answer.
func (c *cliSessionCache) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if revoked, ok := c.lookup(jti); ok {
		return revoked, nil
	}
	// The shared lookup outlives any one caller, so it is detached from the caller's cancellation.
	result, err, _ := c.lookups.Do(jti, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cliSessionLookupTimeout)
		defer cancel()
		revoked, err := c.checker.IsRevoked(lookupCtx, jti)
		if err != nil {
			return false, err
		}
		c.store(jti, revoked)
		return revoked, nil
	})
	if err != nil {
		return false, err
	}
	return result.(bool), nil
}

func (c *cliSessionCache) lookup(jti string) (revoked, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[jti]
	if !ok {
		return false, false
	}
	if !entry.revoked && !c.now().Before(entry.expiresAt) {
		delete(c.entries, jti)
		return false, false
	}
	return entry.revoked, true
}

func (c *cliSessionCache) store(jti string, revoked bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.entries[jti]
	if ok && existing.revoked {
		return
	}
	if !ok && len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	entry := cliSessionCacheEntry{revoked: revoked}
	if !revoked {
		entry.expiresAt = c.now().Add(cliSessionActiveTTL)
	}
	c.entries[jti] = entry
}

// evictLocked drops the expired entries and, if the cache is still full, one arbitrary entry.
// Evicting a live entry only costs another lookup. It scans the whole map, at most
// cliSessionCacheSize entries, and only runs when a new session is cached into a full cache.
func (c *cliSessionCache) evictLocked() {
	now := c.now()
	for jti, entry := range c.entries {
		if !entry.revoked && !now.Before(entry.expiresAt) {
			delete(c.entries, jti)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}
	for jti := range c.entries {
		delete(c.entries, jti)
		return
	}
}
