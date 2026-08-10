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

func TestGetLocalQueuesForClusterQueue(t *testing.T) {
	c := NewKueueCache()

	lq1 := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tpu-queue-1",
			Namespace: "team-a",
		},
		Spec: kueuev1beta1.LocalQueueSpec{
			ClusterQueue: "tpu-cluster-queue",
		},
	}

	lq2 := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tpu-queue-2",
			Namespace: "team-a",
		},
		Spec: kueuev1beta1.LocalQueueSpec{
			ClusterQueue: "tpu-cluster-queue",
		},
	}

	lqDup := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tpu-queue-1",
			Namespace: "team-b",
		},
		Spec: kueuev1beta1.LocalQueueSpec{
			ClusterQueue: "tpu-cluster-queue",
		},
	}

	lq3 := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cpu-queue-1",
			Namespace: "default",
		},
		Spec: kueuev1beta1.LocalQueueSpec{
			ClusterQueue: "cpu-cluster-queue",
		},
	}

	c.LocalQueues.Set("team-a/tpu-queue-1", lq1)
	c.LocalQueues.Set("team-a/tpu-queue-2", lq2)
	c.LocalQueues.Set("team-b/tpu-queue-1", lqDup)
	c.LocalQueues.Set("default/cpu-queue-1", lq3)

	tpuLocalQueues := c.GetLocalQueuesForClusterQueue("tpu-cluster-queue")
	if len(tpuLocalQueues) != 2 || tpuLocalQueues[0] != "tpu-queue-1" || tpuLocalQueues[1] != "tpu-queue-2" {
		t.Fatalf("expected ['tpu-queue-1', 'tpu-queue-2'] for tpu-cluster-queue, got %v", tpuLocalQueues)
	}

	cpuLocalQueues := c.GetLocalQueuesForClusterQueue("cpu-cluster-queue")
	if len(cpuLocalQueues) != 1 || cpuLocalQueues[0] != "cpu-queue-1" {
		t.Fatalf("expected ['cpu-queue-1'] for cpu-cluster-queue, got %v", cpuLocalQueues)
	}

	emptyLocalQueues := c.GetLocalQueuesForClusterQueue("non-existent-cluster-queue")
	if len(emptyLocalQueues) != 0 {
		t.Fatalf("expected 0 local queues for non-existent-cluster-queue, got %d", len(emptyLocalQueues))
	}
}
