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

	"github.com/google/xmc/cmd/rod_data_provider/cache"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlreq "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

func TestClusterQueueReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	kueuev1beta1.AddToScheme(scheme)

	cq := &kueuev1beta1.ClusterQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "cq-test"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cq).Build()
	c := cache.NewResourceCache[*kueuev1beta1.ClusterQueue]()

	rec := NewClusterQueueReconciler(client, c)
	ctx := context.Background()

	req := ctrlreq.Request{NamespacedName: ctrl.ObjectKey{Name: "cq-test"}}
	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if _, ok := c.Get("cq-test"); !ok {
		t.Errorf("expected cq-test to be cached")
	}

	// Test Update scenario
	cqUpdate := cq.DeepCopy()
	cqUpdate.Status.PendingWorkloads = 5
	if err := client.Update(ctx, cqUpdate); err != nil {
		t.Fatalf("Client update failed: %v", err)
	}

	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile update failed: %v", err)
	}

	cachedUpdated, ok := c.Get("cq-test")
	if !ok {
		t.Fatalf("expected cq-test to remain cached after update")
	}
	if cachedUpdated.Status.PendingWorkloads != 5 {
		t.Errorf("expected cached PendingWorkloads = 5, got %d", cachedUpdated.Status.PendingWorkloads)
	}

	// Test Delete scenario
	reqNotFound := ctrlreq.Request{NamespacedName: ctrl.ObjectKey{Name: "cq-test"}}
	client.Delete(ctx, cqUpdate)
	if _, err := rec.Reconcile(ctx, reqNotFound); err != nil {
		t.Fatalf("Reconcile delete failed: %v", err)
	}

	if _, ok := c.Get("cq-test"); ok {
		t.Errorf("expected cq-test to be deleted from cache")
	}
}

func TestLocalQueueReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	kueuev1beta1.AddToScheme(scheme)

	lq := &kueuev1beta1.LocalQueue{
		ObjectMeta: metav1.ObjectMeta{Name: "lq-test", Namespace: "default"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(lq).Build()
	c := cache.NewResourceCache[*kueuev1beta1.LocalQueue]()

	rec := NewLocalQueueReconciler(client, c)
	ctx := context.Background()

	req := ctrlreq.Request{NamespacedName: ctrl.ObjectKey{Name: "lq-test", Namespace: "default"}}
	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if _, ok := c.Get("default/lq-test"); !ok {
		t.Errorf("expected default/lq-test to be cached")
	}

	client.Delete(ctx, lq)
	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile delete failed: %v", err)
	}

	if _, ok := c.Get("default/lq-test"); ok {
		t.Errorf("expected default/lq-test to be deleted from cache")
	}
}

func TestResourceFlavorReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	kueuev1beta1.AddToScheme(scheme)

	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "rf-test"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rf).Build()
	c := cache.NewResourceCache[*kueuev1beta1.ResourceFlavor]()

	rec := NewResourceFlavorReconciler(client, c)
	ctx := context.Background()

	req := ctrlreq.Request{NamespacedName: ctrl.ObjectKey{Name: "rf-test"}}
	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if _, ok := c.Get("rf-test"); !ok {
		t.Errorf("expected rf-test to be cached")
	}

	client.Delete(ctx, rf)
	if _, err := rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile delete failed: %v", err)
	}

	if _, ok := c.Get("rf-test"); ok {
		t.Errorf("expected rf-test to be deleted from cache")
	}
}
