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
package main

import (
	"context"
	"testing"

	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue"
)

type fakeControllerManager struct {
	ctrl.Manager
	client *fake.ClientBuilder
	scheme *runtime.Scheme
}

func TestRegisterControllers_Validation(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	v1alpha2.AddToScheme(scheme)
	v1beta1.AddToScheme(scheme)

	k8sClient := k8sclient.NewWithClients(nil, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil, nil)
	cqCache := clusterqueue.NewNameCache()

	t.Run("nil manager returns error", func(t *testing.T) {
		_, _, err := registerControllers(context.Background(), controllerRegistrationParams{
			mgr:       nil,
			k8sClient: k8sClient,
			cqCache:   cqCache,
		})
		if err == nil {
			t.Fatal("registerControllers(nil mgr) expected error, got nil")
		}
	})

	t.Run("nil k8sClient returns error", func(t *testing.T) {
		mgr := &fakeControllerManager{scheme: scheme}
		_, _, err := registerControllers(context.Background(), controllerRegistrationParams{
			mgr:       mgr,
			k8sClient: nil,
			cqCache:   cqCache,
		})
		if err == nil {
			t.Fatal("registerControllers(nil k8sClient) expected error, got nil")
		}
	})
}

func TestNewMLRunCreator(t *testing.T) {
	ctx := context.Background()
	client := newMLRunCreator(ctx, "test-proj", "test-env", "us-central1")
	// Hypercompute client creation returns nil or valid client without panic.
	if client == nil {
		t.Log("newMLRunCreator returned nil interface in test environment as expected")
	}
}
