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
	"errors"
	"testing"

	"xmanager_cloud/cmd/rod_data_provider/cache"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var errAPIUnavailable = errors.New("api unavailable")

func gpuList(n string) corev1.ResourceList {
	return corev1.ResourceList{gpuResource: resource.MustParse(n)}
}

func gpuContainer(n string) corev1.Container {
	return corev1.Container{Name: "c-" + n, Resources: corev1.ResourceRequirements{Requests: gpuList(n)}}
}

func newPhysicalReconciler(t *testing.T, failGet bool, objs ...client.Object) *PhysicalInventoryReconciler {
	t.Helper()
	b := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...)
	if failGet {
		b = b.WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return errAPIUnavailable
			},
		})
	}
	return &PhysicalInventoryReconciler{Client: b.Build(), Cache: cache.NewPhysicalInventoryCache()}
}

func TestReconcileNode(t *testing.T) {
	const name = "gpu-node-a"
	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Capacity: gpuList("4")},
	}
	cpuNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}

	tests := []struct {
		name       string
		obj        client.Object
		failGet    bool
		preCached  bool
		wantErr    bool
		wantCached bool
	}{
		{name: "gpu node is cached", obj: gpuNode, wantCached: true},
		{name: "non-gpu node evicts cached entry", obj: cpuNode, preCached: true},
		{name: "not found evicts cached entry", preCached: true},
		{name: "get error is propagated and cache kept", failGet: true, preCached: true, wantErr: true, wantCached: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			if tt.obj != nil {
				objs = append(objs, tt.obj)
			}
			r := newPhysicalReconciler(t, tt.failGet, objs...)
			if tt.preCached {
				r.Cache.Nodes.Set(name, gpuNode.DeepCopy())
			}

			_, err := r.ReconcileNode(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("ReconcileNode() error = %v, wantErr %v", err, tt.wantErr)
			}
			if _, got := r.Cache.Nodes.Get(name); got != tt.wantCached {
				t.Errorf("node cached = %v, want %v", got, tt.wantCached)
			}
		})
	}
}

func TestReconcilePod(t *testing.T) {
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "gpu-pod-a", Namespace: "default"}}
	pod := func(nodeName string, phase corev1.PodPhase, containers ...corev1.Container) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace},
			Spec:       corev1.PodSpec{NodeName: nodeName, Containers: containers},
			Status:     corev1.PodStatus{Phase: phase},
		}
	}
	initOnly := pod("gpu-node-a", corev1.PodPending, corev1.Container{Name: "main"})
	initOnly.Spec.InitContainers = []corev1.Container{gpuContainer("1")}

	tests := []struct {
		name       string
		obj        client.Object
		failGet    bool
		preCached  bool
		wantErr    bool
		wantCached bool
	}{
		{name: "running gpu pod is cached", obj: pod("gpu-node-a", corev1.PodRunning, gpuContainer("2")), wantCached: true},
		{name: "multi-container gpu pod is cached", obj: pod("gpu-node-a", corev1.PodRunning, gpuContainer("1"), gpuContainer("1")), wantCached: true},
		{name: "init-container-only gpu pod is cached", obj: initOnly, wantCached: true},
		{name: "unscheduled pod evicts cached entry", obj: pod("", corev1.PodPending, gpuContainer("1")), preCached: true},
		{name: "succeeded pod evicts cached entry", obj: pod("gpu-node-a", corev1.PodSucceeded, gpuContainer("1")), preCached: true},
		{name: "failed pod evicts cached entry", obj: pod("gpu-node-a", corev1.PodFailed, gpuContainer("1")), preCached: true},
		{name: "zero-gpu pod evicts cached entry", obj: pod("gpu-node-a", corev1.PodRunning, corev1.Container{Name: "main"}), preCached: true},
		{name: "not found evicts cached entry", preCached: true},
		{name: "get error is propagated and cache kept", failGet: true, preCached: true, wantErr: true, wantCached: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			if tt.obj != nil {
				objs = append(objs, tt.obj)
			}
			r := newPhysicalReconciler(t, tt.failGet, objs...)
			if tt.preCached {
				r.Cache.Pods.Set(req.String(), pod("gpu-node-a", corev1.PodRunning, gpuContainer("1")))
			}

			_, err := r.ReconcilePod(context.Background(), req)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("ReconcilePod() error = %v, wantErr %v", err, tt.wantErr)
			}
			if _, got := r.Cache.Pods.Get(req.String()); got != tt.wantCached {
				t.Errorf("pod cached = %v, want %v", got, tt.wantCached)
			}
		})
	}
}

func TestPodRequestedGPUs(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	sidecar := gpuContainer("1")
	sidecar.RestartPolicy = &always
	limitOnly := corev1.Container{Name: "limit", Resources: corev1.ResourceRequirements{Limits: gpuList("3")}}

	tests := []struct {
		name       string
		containers []corev1.Container
		inits      []corev1.Container
		want       int64
	}{
		{name: "no gpus", containers: []corev1.Container{{Name: "main"}}, want: 0},
		{name: "sum of regular containers", containers: []corev1.Container{gpuContainer("2"), gpuContainer("1")}, want: 3},
		{name: "limit used when request unset", containers: []corev1.Container{limitOnly}, want: 3},
		{name: "init container larger than regular sum", containers: []corev1.Container{gpuContainer("1")}, inits: []corev1.Container{gpuContainer("4")}, want: 4},
		{name: "regular sum larger than init container", containers: []corev1.Container{gpuContainer("2"), gpuContainer("2")}, inits: []corev1.Container{gpuContainer("3")}, want: 4},
		{name: "sidecar counted with regular containers", containers: []corev1.Container{gpuContainer("2")}, inits: []corev1.Container{sidecar}, want: 3},
		{name: "init after sidecar includes sidecar", containers: []corev1.Container{gpuContainer("1")}, inits: []corev1.Container{sidecar, gpuContainer("4")}, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &corev1.Pod{Spec: corev1.PodSpec{Containers: tt.containers, InitContainers: tt.inits}}
			if got := podRequestedGPUs(p); got != tt.want {
				t.Errorf("podRequestedGPUs() = %d, want %d", got, tt.want)
			}
		})
	}
}
