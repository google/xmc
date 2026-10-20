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
	"k8s.io/apimachinery/pkg/util/cache"
)

const (
	// cliSessionActiveTTL bounds how long an active session is cached, and so how long a revocation
	// takes to reach every replica of every service.
	cliSessionActiveTTL = 60 * time.Second
	// cliSessionRevokedTTL is the cache TTL for revoked sessions; revocation is permanent for the
	// lifetime of any CLI session token.
	cliSessionRevokedTTL = 10 * 365 * 24 * time.Hour
	// cliSessionCacheSize bounds the number of cached sessions.
	cliSessionCacheSize = 10000
	// cliSessionLookupTimeout bounds a session lookup shared by concurrent requests.
	cliSessionLookupTimeout = 10 * time.Second
)

// cliSessionCache is a CLISessionChecker that caches the answers of another one in a bounded
// LRU-with-TTL cache, so most requests do not reach the session store. Active sessions are cached
// for cliSessionActiveTTL. Revocation is permanent, so revoked sessions are never downgraded to
// active. Lookup errors are not cached. Concurrent misses for one session share a single lookup.
type cliSessionCache struct {
	checker CLISessionChecker
	lookups singleflight.Group

	mu      sync.Mutex
	entries *cache.LRUExpireCache
}

var _ CLISessionChecker = (*cliSessionCache)(nil)

// newCLISessionCache returns a cache in front of checker, which must not be nil.
func newCLISessionCache(checker CLISessionChecker) *cliSessionCache {
	return &cliSessionCache{
		checker: checker,
		entries: cache.NewLRUExpireCache(cliSessionCacheSize),
	}
}

// IsRevoked returns the cached answer for jti, or asks the underlying checker and caches its answer.
func (c *cliSessionCache) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if val, ok := c.entries.Get(jti); ok {
		return val.(bool), nil
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

// markRevoked caches jti as revoked, for revocations made by this process.
func (c *cliSessionCache) markRevoked(jti string) {
	c.store(jti, true)
}

func (c *cliSessionCache) store(jti string, revoked bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries.Get(jti); ok && existing.(bool) {
		return
	}
	ttl := cliSessionActiveTTL
	if revoked {
		ttl = cliSessionRevokedTTL
	}
	c.entries.Add(jti, revoked, ttl)
}
