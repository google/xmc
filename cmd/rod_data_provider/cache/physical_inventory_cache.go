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
	corev1 "k8s.io/api/core/v1"
)

// PhysicalInventoryCache encapsulates caches for Nodes and Pods needed for ROD screen 3.
type PhysicalInventoryCache struct {
	Nodes *ResourceCache[*corev1.Node]
	Pods  *ResourceCache[*corev1.Pod]
}

// NewPhysicalInventoryCache creates a new instance of PhysicalInventoryCache.
func NewPhysicalInventoryCache() *PhysicalInventoryCache {
	return &PhysicalInventoryCache{
		Nodes: NewResourceCache[*corev1.Node](),
		Pods:  NewResourceCache[*corev1.Pod](),
	}
}
