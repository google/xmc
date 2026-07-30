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

package main

import (
	"sync"

	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// KueueCache maintains an in-memory thread-safe cache of ClusterQueue, LocalQueue, and ResourceFlavor CRDs.
type KueueCache struct {
	mu              sync.RWMutex
	clusterQueues   map[string]*kueuev1beta1.ClusterQueue
	localQueues     map[string]*kueuev1beta1.LocalQueue
	resourceFlavors map[string]*kueuev1beta1.ResourceFlavor
}

// NewKueueCache initializes an empty KueueCache.
func NewKueueCache() *KueueCache {
	return &KueueCache{
		clusterQueues:   make(map[string]*kueuev1beta1.ClusterQueue),
		localQueues:     make(map[string]*kueuev1beta1.LocalQueue),
		resourceFlavors: make(map[string]*kueuev1beta1.ResourceFlavor),
	}
}

func (c *KueueCache) SetClusterQueue(cq *kueuev1beta1.ClusterQueue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clusterQueues[cq.Name] = cq.DeepCopy()
}

func (c *KueueCache) DeleteClusterQueue(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clusterQueues, name)
}

func (c *KueueCache) SetLocalQueue(lq *kueuev1beta1.LocalQueue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := lq.Namespace + "/" + lq.Name
	c.localQueues[key] = lq.DeepCopy()
}

func (c *KueueCache) DeleteLocalQueue(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.localQueues, namespace+"/"+name)
}

func (c *KueueCache) SetResourceFlavor(rf *kueuev1beta1.ResourceFlavor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resourceFlavors[rf.Name] = rf.DeepCopy()
}

func (c *KueueCache) DeleteResourceFlavor(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.resourceFlavors, name)
}

func (c *KueueCache) GetClusterQueues() []*kueuev1beta1.ClusterQueue {
	c.mu.RLock()
	defer c.mu.RUnlock()
	list := make([]*kueuev1beta1.ClusterQueue, 0, len(c.clusterQueues))
	for _, cq := range c.clusterQueues {
		list = append(list, cq.DeepCopy())
	}
	return list
}

func (c *KueueCache) GetLocalQueues() []*kueuev1beta1.LocalQueue {
	c.mu.RLock()
	defer c.mu.RUnlock()
	list := make([]*kueuev1beta1.LocalQueue, 0, len(c.localQueues))
	for _, lq := range c.localQueues {
		list = append(list, lq.DeepCopy())
	}
	return list
}

func (c *KueueCache) GetResourceFlavors() map[string]*kueuev1beta1.ResourceFlavor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := make(map[string]*kueuev1beta1.ResourceFlavor, len(c.resourceFlavors))
	for k, rf := range c.resourceFlavors {
		m[k] = rf.DeepCopy()
	}
	return m
}
