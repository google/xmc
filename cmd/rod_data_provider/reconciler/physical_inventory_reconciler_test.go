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

package reconciler

import (
	"context"
	"testing"

	"xmanager_cloud/cmd/rod_data_provider/cache"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("4"),
			},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(node).Build()
	c := cache.NewPhysicalInventoryCache()
	r := &PhysicalInventoryReconciler{Client: client, Cache: c}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "gpu-node"}}
	if _, err := r.ReconcileNode(context.Background(), req); err != nil {
		t.Fatalf("ReconcileNode failed: %v", err)
	}

	if _, ok := c.Nodes.Get("gpu-node"); !ok {
		t.Errorf("Expected node to be cached")
	}
}

func TestReconcilePod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("2"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod).Build()
	c := cache.NewPhysicalInventoryCache()
	r := &PhysicalInventoryReconciler{Client: client, Cache: c}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "gpu-pod", Namespace: "default"}}
	if _, err := r.ReconcilePod(context.Background(), req); err != nil {
		t.Fatalf("ReconcilePod failed: %v", err)
	}

	gotPod, ok := c.Pods.Get(req.String())
	if !ok {
		t.Fatalf("Expected pod to be cached")
	}

	if gotPod.Name != "gpu-pod" {
		t.Errorf("Expected cached pod name to be 'gpu-pod', got %q", gotPod.Name)
	}
}
