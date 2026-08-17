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
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	clientgoscheme.AddToScheme(s)
	kueuev1beta1.AddToScheme(s)
	return s
}

func TestNewReconciler_Validation(t *testing.T) {
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	cache := NewNameCache()

	tests := []struct {
		name    string
		params  Params
		wantErr bool
	}{
		{
			name: "valid params",
			params: Params{
				Client: cl,
				Scheme: scheme,
				Cache:  cache,
			},
			wantErr: false,
		},
		{
			name: "missing client",
			params: Params{
				Scheme: scheme,
				Cache:  cache,
			},
			wantErr: true,
		},
		{
			name: "missing scheme",
			params: Params{
				Client: cl,
				Cache:  cache,
			},
			wantErr: true,
		},
		{
			name: "missing cache",
			params: Params{
				Client: cl,
				Scheme: scheme,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := NewReconciler(tc.params)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewReconciler() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && rec == nil {
				t.Fatal("NewReconciler() returned nil reconciler without error")
			}
		})
	}
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
	rec, err := NewReconciler(Params{
		Client: cl,
		Scheme: scheme,
		Cache:  cache,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	_, err = rec.Reconcile(context.Background(), ctrl.Request{
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
	rec, err := NewReconciler(Params{
		Client: cl,
		Scheme: scheme,
		Cache:  cache,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	_, err = rec.Reconcile(context.Background(), ctrl.Request{
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
