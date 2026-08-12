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

	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// KueueCache holds 3 instances of generic ResourceCache for ClusterQueue, LocalQueue, and ResourceFlavor.
type KueueCache struct {
	ClusterQueues   *ResourceCache[*kueuev1beta1.ClusterQueue]
	LocalQueues     *ResourceCache[*kueuev1beta1.LocalQueue]
	ResourceFlavors *ResourceCache[*kueuev1beta1.ResourceFlavor]
}

// NewKueueCache initializes a KueueCache with 3 generic ResourceCache instances.
func NewKueueCache() *KueueCache {
	return &KueueCache{
		ClusterQueues:   NewResourceCache[*kueuev1beta1.ClusterQueue](),
		LocalQueues:     NewResourceCache[*kueuev1beta1.LocalQueue](),
		ResourceFlavors: NewResourceCache[*kueuev1beta1.ResourceFlavor](),
	}
}

// GetLocalQueuesForClusterQueue returns the sorted names of all LocalQueues connected to the given ClusterQueue.
func (c *KueueCache) GetLocalQueuesForClusterQueue(clusterQueueName string) []string {
	if c == nil || c.LocalQueues == nil || clusterQueueName == "" {
		return nil
	}

	seen := make(map[string]bool)
	var matchedNames []string
	localQueues := c.LocalQueues.List()
	for _, lq := range localQueues {
		if lq == nil {
			continue
		}
		if string(lq.Spec.ClusterQueue) == clusterQueueName && !seen[lq.Name] {
			seen[lq.Name] = true
			matchedNames = append(matchedNames, lq.Name)
		}
	}
	sort.Strings(matchedNames)
	return matchedNames
}
