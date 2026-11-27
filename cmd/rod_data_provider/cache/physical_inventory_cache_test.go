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

	corev1 "k8s.io/api/core/v1"
)

func TestNewPhysicalInventoryCache(t *testing.T) {
	c := NewPhysicalInventoryCache()
	if c == nil {
		t.Fatal("Expected PhysicalInventoryCache to be created")
	}
	if c.Nodes == nil {
		t.Error("Expected Nodes cache to be initialized")
	}
	if c.Pods == nil {
		t.Error("Expected Pods cache to be initialized")
	}

	node := &corev1.Node{}
	c.Nodes.Set("node-1", node)
	gotNode, ok := c.Nodes.Get("node-1")
	if !ok || gotNode != node {
		t.Errorf("Expected node-1 to be retrievable")
	}

	pod := &corev1.Pod{}
	c.Pods.Set("pod-1", pod)
	gotPod, ok := c.Pods.Get("pod-1")
	if !ok || gotPod != pod {
		t.Errorf("Expected pod-1 to be retrievable")
	}
}
