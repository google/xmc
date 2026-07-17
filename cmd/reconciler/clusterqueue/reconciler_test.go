// Copyright 2025 Google LLC
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
package clusterqueue

import (
	"context"
	"testing"

	"golang/cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	clientgoscheme.AddToScheme(s)
	kueuev1beta1.AddToScheme(s)
	return s
}

func TestClusterQueueReconciler(t *testing.T) {
	scheme := newScheme()
	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-clusterqueue",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{cq}...).Build()
	cache := NewNameCache()
	rec := &Reconciler{
		Client: cl,
		Scheme: scheme,
		Cache:  cache,
	}

	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "test-clusterqueue",
		},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned unexpected error: %v", err)
	}

	want := []string{"test-clusterqueue"}
	got := cache.Names()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Cache contents differ (-want +got):\n%s", diff)
	}
}

func TestClusterQueueReconciler_NotFound(t *testing.T) {
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{}...).Build()
	cache := NewNameCache()
	cache.Add("test-clusterqueue")
	rec := &Reconciler{
		Client: cl,
		Scheme: scheme,
		Cache:  cache,
	}

	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "test-clusterqueue",
		},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned unexpected error: %v", err)
	}

	if len(cache.Names()) != 0 {
		t.Errorf("Expected cache to be empty, got %v", cache.Names())
	}
}
