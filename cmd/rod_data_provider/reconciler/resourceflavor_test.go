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

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	ctrlreq "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func TestResourceFlavorReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	kueuev1beta1.AddToScheme(scheme)

	rf := &kueuev1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: "rf-test"},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rf).Build()
	c := cache.NewResourceCache[*kueuev1beta1.ResourceFlavor]()

	rec := &ResourceFlavorReconciler{Client: client, Cache: c}
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
