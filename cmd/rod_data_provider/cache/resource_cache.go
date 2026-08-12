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
	"sort"
	"sync"
	"sync/atomic"
)

// ResourceCache is an arbitrary, thread-safe, generic in-memory cache for any object type T.
// Optimized for read-heavy workloads: Get() and List() are 100% lock-free (zero mutex acquisition).
//
// Note: If T is a pointer type, the cache returns direct references to the stored objects.
// Callers must not modify retrieved objects directly, or should perform a deep copy to avoid race conditions.
type ResourceCache[T any] struct {
	mu           sync.Mutex
	mapSnapshot  atomic.Pointer[map[string]T]
	listSnapshot atomic.Pointer[[]T]
}

// buildSortedList returns a slice of values from the map sorted deterministically by key name.
func buildSortedList[T any](m map[string]T) []T {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	list := make([]T, 0, len(m))
	for _, k := range keys {
		list = append(list, m[k])
	}
	return list
}

// NewResourceCache initializes a new empty ResourceCache for type T.
func NewResourceCache[T any]() *ResourceCache[T] {
	c := &ResourceCache[T]{}
	emptyMap := make(map[string]T)
	emptyList := make([]T, 0)
	c.mapSnapshot.Store(&emptyMap)
	c.listSnapshot.Store(&emptyList)
	return c
}

// Set stores a key-value pair and atomically updates map and list snapshots.
func (c *ResourceCache[T]) Set(key string, val T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currMap := c.mapSnapshot.Load()
	var newMap map[string]T
	if currMap == nil {
		newMap = make(map[string]T, 1)
	} else {
		newMap = make(map[string]T, len(*currMap)+1)
		for k, v := range *currMap {
			newMap[k] = v
		}
	}
	newMap[key] = val

	newList := buildSortedList(newMap)

	c.mapSnapshot.Store(&newMap)
	c.listSnapshot.Store(&newList)
}

// Delete removes an item by key from the cache and atomically updates map and list snapshots.
func (c *ResourceCache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currMap := c.mapSnapshot.Load()
	if currMap == nil {
		return
	}
	if _, exists := (*currMap)[key]; !exists {
		return
	}

	newMap := make(map[string]T, len(*currMap)-1)
	for k, v := range *currMap {
		if k != key {
			newMap[k] = v
		}
	}

	newList := buildSortedList(newMap)

	c.mapSnapshot.Store(&newMap)
	c.listSnapshot.Store(&newList)
}

// Get retrieves an item by key from the cache without acquiring any locks (100% Lock-Free O(1)).
func (c *ResourceCache[T]) Get(key string) (T, bool) {
	m := c.mapSnapshot.Load()
	if m == nil {
		var zero T
		return zero, false
	}
	val, ok := (*m)[key]
	return val, ok
}

// List returns all cached items as a slice without acquiring any locks (100% Lock-Free O(1)).
func (c *ResourceCache[T]) List() []T {
	if ptr := c.listSnapshot.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

// Map returns a copy of the internal map of key-value pairs without acquiring any locks (100% Lock-Free).
func (c *ResourceCache[T]) Map() map[string]T {
	m := c.mapSnapshot.Load()
	if m == nil {
		return make(map[string]T)
	}
	cp := make(map[string]T, len(*m))
	for k, v := range *m {
		cp[k] = v
	}
	return cp
}
