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
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/cmd/reconciler/clusterqueue"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

// fakeControllerManager records runnables added via Add and serves a fake
// client; all other ctrl.Manager methods panic if called.
type fakeControllerManager struct {
	ctrl.Manager
	client    client.Client
	scheme    *runtime.Scheme
	addErr    error
	runnables []manager.Runnable
}

func (f *fakeControllerManager) GetClient() client.Client   { return f.client }
func (f *fakeControllerManager) GetScheme() *runtime.Scheme { return f.scheme }

func (f *fakeControllerManager) Add(r manager.Runnable) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.runnables = append(f.runnables, r)
	return nil
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	builder := runtime.NewSchemeBuilder(clientgoscheme.AddToScheme, v1alpha2.AddToScheme, v1beta1.AddToScheme)
	if err := builder.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() failed: %v", err)
	}
	return scheme
}

func TestRegisterControllers_Validation(t *testing.T) {
	scheme := newTestScheme(t)
	k8sClient := k8sclient.NewWithClients(nil, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil)
	cqCache := clusterqueue.NewNameCache()

	t.Run("nil manager returns error", func(t *testing.T) {
		_, err := registerControllers(context.Background(), controllerRegistrationParams{
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
		_, err := registerControllers(context.Background(), controllerRegistrationParams{
			mgr:       mgr,
			k8sClient: nil,
			cqCache:   cqCache,
		})
		if err == nil {
			t.Fatal("registerControllers(nil k8sClient) expected error, got nil")
		}
	})
}

func TestAddBackgroundRunnables(t *testing.T) {
	scheme := newTestScheme(t)
	withClientset := k8sclient.NewWithClients(kubernetesfake.NewClientset(), nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil)
	withoutClientset := k8sclient.NewWithClients(nil, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil)
	batcher := shared.NewUpdateBatcher(context.Background(), &shared.BatcherConfig{}, nil)

	tests := []struct {
		name          string
		k8sClient     *k8sclient.Client
		addErr        error
		wantErr       bool
		wantRunnables int
	}{
		{name: "adds batcher and kueue sync", k8sClient: withClientset, wantRunnables: 2},
		{name: "kueuesync construction fails", k8sClient: withoutClientset, wantErr: true},
		{name: "manager Add fails", k8sClient: withClientset, addErr: errors.New("add failed"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &fakeControllerManager{
				client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				scheme: scheme,
				addErr: tc.addErr,
			}
			err := addBackgroundRunnables(controllerRegistrationParams{
				mgr:       mgr,
				k8sClient: tc.k8sClient,
				cqCache:   clusterqueue.NewNameCache(),
			}, batcher)
			if (err != nil) != tc.wantErr {
				t.Fatalf("addBackgroundRunnables() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got := len(mgr.runnables); got != tc.wantRunnables {
				t.Errorf("addBackgroundRunnables() added %d runnables, want %d", got, tc.wantRunnables)
			}
		})
	}
}

func TestNewMLRunCreator(t *testing.T) {
	ctx := context.Background()
	client := newMLRunCreator(ctx, "test-proj", "test-env", "us-central1")
	// Hypercompute client creation returns nil or valid client without panic.
	if client == nil {
		t.Log("newMLRunCreator returned nil interface in test environment as expected")
	}
}

func TestNewJobSetStorage(t *testing.T) {
	if got := newJobSetStorage(nil, workunit.NewUpdater(nil)); got != nil {
		t.Errorf("newJobSetStorage(nil, ...) = %v, want nil interface", got)
	}
	store := &storage.MockStorage{}
	if got := newJobSetStorage(store, workunit.NewUpdater(store)); got == nil {
		t.Error("newJobSetStorage(non-nil, ...) = nil, want non-nil ReconcilerStorage")
	}
}
