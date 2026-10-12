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
package k8sclient

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCheckNamespaceExists(t *testing.T) {
	ctx := context.Background()
	client := &Client{
		clientset: kubernetesfake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "existing-ns"}}),
	}

	exists, err := client.checkNamespaceExists(ctx, "existing-ns")
	if err != nil {
		t.Fatalf("checkNamespaceExists(%q) returned unexpected error: %v", "existing-ns", err)
	}
	if !exists {
		t.Errorf("checkNamespaceExists(%q) = false, want true", "existing-ns")
	}

	exists, err = client.checkNamespaceExists(ctx, "non-existing-ns")
	if err != nil {
		t.Fatalf("checkNamespaceExists(%q) returned unexpected error: %v", "non-existing-ns", err)
	}
	if exists {
		t.Errorf("checkNamespaceExists(%q) = true, want false", "non-existing-ns")
	}
}

func TestCreateNamespace(t *testing.T) {
	ctx := context.Background()
	clientset := kubernetesfake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
	}

	err := client.createNamespace(ctx, "new-ns")
	if err != nil {
		t.Fatalf("createNamespace(%q) returned unexpected error: %v", "new-ns", err)
	}

	ns, err := clientset.CoreV1().Namespaces().Get(ctx, "new-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get namespace %q after creation: %v", "new-ns", err)
	}
	if ns.Name != "new-ns" {
		t.Errorf("Namespace name = %q, want %q", ns.Name, "new-ns")
	}
	if ns.Labels["managed-by"] != "xmc-actuator" {
		t.Errorf("Namespace label 'managed-by' = %q, want %q", ns.Labels["managed-by"], "xmc-actuator")
	}
}

func TestEnsureNamespaceExists_Exists(t *testing.T) {
	ctx := context.Background()
	clientset := kubernetesfake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "existing-ns"}})
	client := &Client{
		clientset:     clientset,
		RuntimeConfig: ActuatorRuntimeConfig{CreateNewNamespaces: true},
	}

	// Namespace already exists
	err := client.ensureNamespaceExists(ctx, "existing-ns")
	if err != nil {
		t.Fatalf("ensureNamespaceExists(%q) returned unexpected error when namespace exists: %v", "existing-ns", err)
	}
}

func TestEnsureNamespaceExists_Creates(t *testing.T) {
	ctx := context.Background()
	clientset := kubernetesfake.NewSimpleClientset()
	client := &Client{
		clientset:     clientset,
		RuntimeConfig: ActuatorRuntimeConfig{CreateNewNamespaces: true},
	}

	// Namespace does not exist, should be created
	err := client.ensureNamespaceExists(ctx, "new-ns")
	if err != nil {
		t.Fatalf("ensureNamespaceExists(%q) returned unexpected error when namespace does not exist: %v", "new-ns", err)
	}
	_, err = clientset.CoreV1().Namespaces().Get(ctx, "new-ns", metav1.GetOptions{})
	if err != nil {
		t.Errorf("ensureNamespaceExists(%q) failed to create namespace: %v", "new-ns", err)
	}
}

func TestEnsureNamespaceExists_FailsIfCreationDisabled(t *testing.T) {
	ctx := context.Background()
	clientset := kubernetesfake.NewSimpleClientset()
	client := &Client{
		clientset:     clientset,
		RuntimeConfig: ActuatorRuntimeConfig{CreateNewNamespaces: false},
	}

	// Namespace does not exist, should return error
	err := client.ensureNamespaceExists(ctx, "new-ns")
	if err == nil {
		t.Fatalf("ensureNamespaceExists(%q) succeeded, want error when namespace does not exist and creation is disabled", "new-ns")
	}
}

func TestEnsureNamespaceExists_Concurrent(t *testing.T) {
	ctx := context.Background()
	// Race condition - namespace is created concurrently
	clientset := kubernetesfake.NewSimpleClientset()
	client := &Client{
		clientset:     clientset,
		RuntimeConfig: ActuatorRuntimeConfig{CreateNewNamespaces: true},
	}
	clientset.PrependReactor("create", "namespaces", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, "concurrent-ns")
	})
	err := client.ensureNamespaceExists(ctx, "concurrent-ns")
	if err != nil {
		t.Fatalf("ensureNamespaceExists(%q) returned unexpected error during concurrent creation: %v", "concurrent-ns", err)
	}
}
