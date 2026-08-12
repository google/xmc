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
	"testing"

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func TestResourceCache(t *testing.T) {
	cqCache := NewResourceCache[*kueuev1beta1.ClusterQueue]()

	cq1 := &kueuev1beta1.ClusterQueue{ObjectMeta: metav1.ObjectMeta{Name: "cq1"}}
	cq2 := &kueuev1beta1.ClusterQueue{ObjectMeta: metav1.ObjectMeta{Name: "cq2"}}

	cqCache.Set("cq1", cq1)
	cqCache.Set("cq2", cq2)

	if got, ok := cqCache.Get("cq1"); !ok || got.Name != "cq1" {
		t.Errorf("Get(cq1) = %v, %v; want cq1, true", got, ok)
	}

	list := cqCache.List()
	if len(list) != 2 {
		t.Errorf("List() len = %d, want 2", len(list))
	}

	cqMap := cqCache.Map()
	if len(cqMap) != 2 || cqMap["cq2"].Name != "cq2" {
		t.Errorf("Map() = %v, want map containing cq2", cqMap)
	}

	cqCache.Delete("cq1")
	if _, ok := cqCache.Get("cq1"); ok {
		t.Errorf("Get(cq1) after Delete should return false")
	}
	if len(cqCache.List()) != 1 {
		t.Errorf("List() len after Delete = %d, want 1", len(cqCache.List()))
	}
}

func TestResourceCacheConcurrent(t *testing.T) {
	cqCache := NewResourceCache[int]()
	const goroutines = 10
	const iterations = 100

	done := make(chan bool, goroutines*3)

	// Concurrent Writers
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < iterations; j++ {
				cqCache.Set(string(rune('a'+id)), j)
			}
			done <- true
		}(i)
	}

	// Concurrent Readers
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < iterations; j++ {
				cqCache.Get(string(rune('a' + id)))
			}
			done <- true
		}(i)
	}

	// Concurrent Listers/Mappers
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				cqCache.List()
				cqCache.Map()
			}
			done <- true
		}()
	}

	for i := 0; i < goroutines*3; i++ {
		<-done
	}
}

func TestResourceCacheSortedList(t *testing.T) {
	c := NewResourceCache[string]()

	// Insert items out of order
	c.Set("charlie", "c")
	c.Set("alpha", "a")
	c.Set("delta", "d")
	c.Set("bravo", "b")

	list := c.List()
	expected := []string{"a", "b", "c", "d"}
	if len(list) != len(expected) {
		t.Fatalf("List() len = %d, want %d", len(list), len(expected))
	}
	for i, val := range list {
		if val != expected[i] {
			t.Errorf("List()[%d] = %q, want %q", i, val, expected[i])
		}
	}

	// Delete item and verify list remains sorted
	c.Delete("bravo")
	listAfterDelete := c.List()
	expectedAfterDelete := []string{"a", "c", "d"}
	if len(listAfterDelete) != len(expectedAfterDelete) {
		t.Fatalf("List() len after delete = %d, want %d", len(listAfterDelete), len(expectedAfterDelete))
	}
	for i, val := range listAfterDelete {
		if val != expectedAfterDelete[i] {
			t.Errorf("List()[%d] after delete = %q, want %q", i, val, expectedAfterDelete[i])
		}
	}
}
