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

// Package redislock provides single-instance distributed Redis lease locks backed by SET NX PX
// acquisition and token-checked Lua renewal and release scripts.
package redislock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const tokenBytes = 16

var (
	// ErrNotAcquired is returned by Locker.Acquire when the lock key is already held.
	ErrNotAcquired = errors.New("redis lock not acquired")
	// ErrLeaseLost is returned by Lease.Extend and Lease.Release when the lock key expired or changed owner.
	ErrLeaseLost = errors.New("redis lock lease lost")

	extendScript  = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`)
	releaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)
)

// Client is the minimal go-redis interface required by Locker. *redis.Client satisfies it.
type Client interface {
	SetArgs(ctx context.Context, key string, value any, a redis.SetArgs) *redis.StatusCmd
	redis.Scripter
}

var _ Client = (*redis.Client)(nil)

// Locker acquires time-bounded distributed leases in Redis.
type Locker struct {
	client   Client
	ttl      time.Duration
	randRead func([]byte) (int, error)
}

// New validates client and ttl and returns a Locker whose leases start with and extend by ttl.
func New(client Client, ttl time.Duration) (*Locker, error) {
	if isNilClient(client) {
		return nil, errors.New("redis lock client must not be nil")
	}
	if ttl < time.Millisecond {
		return nil, fmt.Errorf("redis lock ttl must be at least 1ms, got %v", ttl)
	}
	return &Locker{client: client, ttl: ttl, randRead: rand.Read}, nil
}

func isNilClient(client Client) bool {
	if client == nil {
		return true
	}
	c, ok := client.(*redis.Client)
	return ok && c == nil
}

// Acquire attempts once to acquire the lock at key without blocking, returning ErrNotAcquired if held.
func (l *Locker) Acquire(ctx context.Context, key string) (*Lease, error) {
	if key == "" {
		return nil, errors.New("redis lock key must not be empty")
	}
	token, err := l.generateToken()
	if err != nil {
		return nil, err
	}
	err = l.client.SetArgs(ctx, key, token, redis.SetArgs{Mode: "NX", TTL: l.ttl}).Err()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotAcquired
	}
	if err != nil {
		return nil, fmt.Errorf("failed to acquire redis lock %q: %w", key, err)
	}
	return &Lease{client: l.client, key: key, token: token, ttlMillis: l.ttl.Milliseconds()}, nil
}

func (l *Locker) generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := l.randRead(buf); err != nil {
		return "", fmt.Errorf("failed to generate redis lock token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Lease represents an acquired lock on a single Redis key.
type Lease struct {
	client    Client
	key       string
	token     string
	ttlMillis int64
}

// Extend resets the lease TTL if and only if the lease token still owns the key.
func (l *Lease) Extend(ctx context.Context) error {
	updated, err := extendScript.Run(ctx, l.client, []string{l.key}, l.token, l.ttlMillis).Int64()
	if err != nil {
		return fmt.Errorf("failed to extend redis lock %q: %w", l.key, err)
	}
	if updated == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Release deletes the lock key if and only if the lease token still owns it.
func (l *Lease) Release(ctx context.Context) error {
	deleted, err := releaseScript.Run(ctx, l.client, []string{l.key}, l.token).Int64()
	if err != nil {
		return fmt.Errorf("failed to release redis lock %q: %w", l.key, err)
	}
	if deleted == 0 {
		return ErrLeaseLost
	}
	return nil
}
