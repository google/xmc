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

package cache

import (
	"sync"
)

// ResourceCache is an arbitrary, thread-safe, generic in-memory cache for any object type T.
type ResourceCache[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

// NewResourceCache initializes a new empty ResourceCache for type T.
func NewResourceCache[T any]() *ResourceCache[T] {
	return &ResourceCache[T]{
		items: make(map[string]T),
	}
}

// Set stores a key-value pair in the cache.
func (c *ResourceCache[T]) Set(key string, val T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = val
}

// Delete removes an item by key from the cache.
func (c *ResourceCache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Get retrieves an item by key from the cache.
func (c *ResourceCache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.items[key]
	return val, ok
}

// List returns all cached items as a slice.
func (c *ResourceCache[T]) List() []T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	list := make([]T, 0, len(c.items))
	for _, item := range c.items {
		list = append(list, item)
	}
	return list
}

// Map returns a copy of the internal map of key-value pairs.
func (c *ResourceCache[T]) Map() map[string]T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]T, len(c.items))
	for k, v := range c.items {
		m[k] = v
	}
	return m
}
