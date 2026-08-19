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

	"github.com/google/go-cmp/cmp"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func TestKueueCacheInitialization(t *testing.T) {
	cache := NewKueueCache()
	if cache.ClusterQueues == nil {
		t.Errorf("ClusterQueues cache is nil")
	}
	if cache.LocalQueues == nil {
		t.Errorf("LocalQueues cache is nil")
	}
	if cache.ResourceFlavors == nil {
		t.Errorf("ResourceFlavors cache is nil")
	}
}

func TestGetLocalQueuesForClusterQueue(t *testing.T) {
	c := NewKueueCache()

	c.LocalQueues.Set("team-a/tpu-queue-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "tpu-queue-1", Namespace: "team-a"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "tpu-cluster-queue"},
	})
	c.LocalQueues.Set("team-a/tpu-queue-2", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "tpu-queue-2", Namespace: "team-a"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "tpu-cluster-queue"},
	})
	c.LocalQueues.Set("team-b/tpu-queue-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "tpu-queue-1", Namespace: "team-b"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "tpu-cluster-queue"},
	})
	c.LocalQueues.Set("default/cpu-queue-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-queue-1", Namespace: "default"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cpu-cluster-queue"},
	})

	t.Run("returns sorted and deduplicated local queues for matching cluster queue", func(t *testing.T) {
		got := c.GetLocalQueuesForClusterQueue("tpu-cluster-queue")
		want := []string{"tpu-queue-1", "tpu-queue-2"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetLocalQueuesForClusterQueue() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns single matching local queue", func(t *testing.T) {
		got := c.GetLocalQueuesForClusterQueue("cpu-cluster-queue")
		want := []string{"cpu-queue-1"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetLocalQueuesForClusterQueue() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns empty slice for non-existent cluster queue", func(t *testing.T) {
		got := c.GetLocalQueuesForClusterQueue("non-existent-cluster-queue")
		if len(got) != 0 {
			t.Errorf("expected 0 local queues for non-existent cluster queue, got %v", got)
		}
	})

	t.Run("returns nil for empty cluster queue name", func(t *testing.T) {
		got := c.GetLocalQueuesForClusterQueue("")
		if got != nil {
			t.Errorf("expected nil for empty clusterQueueName, got %v", got)
		}
	})

	t.Run("returns empty slice when cache contains no local queues", func(t *testing.T) {
		emptyCache := NewKueueCache()
		got := emptyCache.GetLocalQueuesForClusterQueue("tpu-cluster-queue")
		if len(got) != 0 {
			t.Errorf("expected 0 local queues when cache is empty, got %v", got)
		}
	})

	t.Run("returns nil when cache is nil", func(t *testing.T) {
		var nilCache *KueueCache
		got := nilCache.GetLocalQueuesForClusterQueue("tpu-cluster-queue")
		if got != nil {
			t.Errorf("expected nil for nil cache, got %v", got)
		}
	})
}

func TestGetLocalQueuesForClusterQueues(t *testing.T) {
	c := NewKueueCache()
	c.LocalQueues.Set("team-a/lq-1", &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "lq-1", Namespace: "team-a"},
		Spec:       kueuev1beta1.LocalQueueSpec{ClusterQueue: "cq-gpu"},
	})

	t.Run("returns target queues and mappings for cluster queues", func(t *testing.T) {
		targetQueues, mapping := c.GetLocalQueuesForClusterQueues([]string{"cq-gpu", "cq-cpu"})
		wantTargets := []string{"lq-1", "cq-cpu"}
		wantMapping := map[string][]string{
			"cq-gpu": {"lq-1"},
			"cq-cpu": {"cq-cpu"},
		}
		if diff := cmp.Diff(wantTargets, targetQueues); diff != "" {
			t.Errorf("targetQueues mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(wantMapping, mapping); diff != "" {
			t.Errorf("mapping mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("handles nil cache gracefully", func(t *testing.T) {
		var nilCache *KueueCache
		targetQueues, mapping := nilCache.GetLocalQueuesForClusterQueues([]string{"cq-gpu"})
		wantTargets := []string{"cq-gpu"}
		wantMapping := map[string][]string{
			"cq-gpu": {"cq-gpu"},
		}
		if diff := cmp.Diff(wantTargets, targetQueues); diff != "" {
			t.Errorf("targetQueues mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(wantMapping, mapping); diff != "" {
			t.Errorf("mapping mismatch (-want +got):\n%s", diff)
		}
	})
}
