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
	"context"
	"testing"

	rodpb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/cmd/rod_data_provider/cache"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func TestGetResourceAvailability(t *testing.T) {
	s := &server{
		physCache:  cache.NewPhysicalInventoryCache(),
		kueueCache: cache.NewKueueCache(),
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"topology.kubernetes.io/zone":      "us-central1-a",
				"cloud.google.com/gke-accelerator": "nvidia-tesla-v100",
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("2"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	s.physCache.Nodes.Set(node.Name, node)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
	s.physCache.Pods.Set(pod.Name, pod)

	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cq-1"},
		Spec: kueuev1beta1.ClusterQueueSpec{
			ResourceGroups: []kueuev1beta1.ResourceGroup{
				{
					Flavors: []kueuev1beta1.FlavorQuotas{{Name: kueuev1beta1.ResourceFlavorReference("flavor-1")}},
				},
			},
		},
	}
	s.kueueCache.ClusterQueues.Set(cq.Name, cq)

	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-1"},
		Spec: kueuev1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{
				"cloud.google.com/gke-accelerator": "nvidia-tesla-v100",
			},
		},
	}
	s.kueueCache.ResourceFlavors.Set(rf.Name, rf)

	resp, err := s.GetResourceAvailability(context.Background(), &rodpb.GetResourceAvailabilityRequest{})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(resp.Summaries) != 1 {
		t.Fatalf("Expected 1 summary, got %d", len(resp.Summaries))
	}

	summary := resp.Summaries[0]
	if summary.AcceleratorType != "nvidia-tesla-v100" {
		t.Errorf("Expected nvidia-tesla-v100, got %q", summary.AcceleratorType)
	}
	if summary.TotalAccelerators != 2 {
		t.Errorf("Expected 2 total, got %d", summary.TotalAccelerators)
	}
	if summary.AllocatedAccelerators != 1 {
		t.Errorf("Expected 1 allocated, got %d", summary.AllocatedAccelerators)
	}
	if len(summary.ClusterQueues) != 1 || summary.ClusterQueues[0] != "cq-1" {
		t.Errorf("Expected cluster queues [cq-1], got %v", summary.ClusterQueues)
	}
}
