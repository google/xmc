// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestNewK8sWorkloadManager(t *testing.T) {
	t.Run("nil clientset returns error", func(t *testing.T) {
		mgr, err := NewK8sWorkloadManager(nil)
		if err == nil {
			t.Fatal("expected error with nil clientset, got nil")
		}
		if mgr != nil {
			t.Fatal("expected nil manager on error")
		}
	})

	t.Run("valid clientset succeeds", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, err := NewK8sWorkloadManager(clientset)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mgr == nil {
			t.Fatal("expected non-nil manager")
		}
	})
}

func TestEnsure1PWorkload_Validation(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	mgr, err := NewK8sWorkloadManager(clientset)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()

	t.Run("empty namespace", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "", "my-comp", WorkloadOptions{})
		if err == nil || err.Error() != "namespace cannot be empty" {
			t.Fatalf("expected namespace cannot be empty error, got: %v", err)
		}
	})

	t.Run("empty component name", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "", WorkloadOptions{})
		if err == nil || err.Error() != "component name cannot be empty" {
			t.Fatalf("expected component name cannot be empty error, got: %v", err)
		}
	})

	t.Run("invalid port string", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "my-comp", WorkloadOptions{
			Parameters: map[string]string{
				"port": "not-a-number",
			},
		})
		if err == nil {
			t.Fatal("expected error on invalid port string")
		}
	})

	t.Run("out of range port", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "my-comp", WorkloadOptions{
			Parameters: map[string]string{
				"port": "70000",
			},
		})
		if err == nil {
			t.Fatal("expected error on port > 65535")
		}
	})

	t.Run("zero or negative port", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "my-comp", WorkloadOptions{
			Parameters: map[string]string{
				"port": "0",
			},
		})
		if err == nil {
			t.Fatal("expected error on port <= 0")
		}
	})

	t.Run("invalid replicas string", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "my-comp", WorkloadOptions{
			Parameters: map[string]string{
				"replicas": "invalid",
			},
		})
		if err == nil {
			t.Fatal("expected error on invalid replicas string")
		}
	})

	t.Run("negative replicas", func(t *testing.T) {
		err := mgr.Ensure1PWorkload(ctx, "test-ns", "my-comp", WorkloadOptions{
			Parameters: map[string]string{
				"replicas": "-1",
			},
		})
		if err == nil {
			t.Fatal("expected error on negative replicas")
		}
	})
}

func TestEnsure1PWorkload_CreateAndInPlaceUpdate(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	mgr, err := NewK8sWorkloadManager(clientset)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()
	ns := "xmc-system"
	compName := "ingestion-gateway"
	ownerUID := types.UID("uid-12345")

	opts := WorkloadOptions{
		OwnerUID: ownerUID,
		ImageTag: "v1.2.3",
		Parameters: map[string]string{
			"port":                "9090",
			"replicas":            "3",
			"logLevel":            "debug",
			"metrics.enabled":     "true",
			"k8s-cluster-name":    "prod-cluster",
			"special-arg":         "val",
			"drainTimeoutSeconds": "120",
		},
		DrainTimeoutSeconds: 120,
	}

	// 1. Initial Creation Pass
	if err := mgr.Ensure1PWorkload(ctx, ns, compName, opts); err != nil {
		t.Fatalf("Ensure1PWorkload failed: %v", err)
	}

	dep, err := clientset.AppsV1().Deployments(ns).Get(ctx, compName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get created Deployment: %v", err)
	}

	if *dep.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", *dep.Spec.Replicas)
	}
	if len(dep.OwnerReferences) != 1 || dep.OwnerReferences[0].UID != ownerUID {
		t.Errorf("unexpected ownerReferences: %+v", dep.OwnerReferences)
	}
	if dep.OwnerReferences[0].APIVersion != "xmc.dev/v1alpha1" || dep.OwnerReferences[0].Kind != "Component" {
		t.Errorf("unexpected ownerReference apiVersion/kind: %+v", dep.OwnerReferences[0])
	}
	if *dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 120 {
		t.Errorf("expected terminationGracePeriodSeconds=120, got %d", *dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.Name != compName {
		t.Errorf("expected container name %q, got %q", compName, container.Name)
	}
	expectedImg := fmt.Sprintf("%s/%s:v1.2.3", defaultImageRepoPrefix, compName)
	if container.Image != expectedImg {
		t.Errorf("expected image %q, got %q", expectedImg, container.Image)
	}
	if len(container.Ports) != 1 || container.Ports[0].ContainerPort != 9090 {
		t.Errorf("expected port 9090, got %+v", container.Ports)
	}
	if container.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("expected TCP protocol on container port, got %v", container.Ports[0].Protocol)
	}

	// Verify preStop hook
	if container.Lifecycle == nil || container.Lifecycle.PreStop == nil || container.Lifecycle.PreStop.Exec == nil {
		t.Fatalf("expected preStop exec hook configured")
	}

	svc, err := clientset.CoreV1().Services(ns).Get(ctx, compName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get created Service: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 9090 {
		t.Errorf("expected service port 9090, got %+v", svc.Spec.Ports)
	}
	if svc.Spec.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("expected TCP protocol on service port, got %v", svc.Spec.Ports[0].Protocol)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("expected ClusterIP service type, got %v", svc.Spec.Type)
	}

	// Simulate Kubernetes setting ClusterIP on the Service
	svc.Spec.ClusterIP = "10.96.0.42"
	svc.Spec.ClusterIPs = []string{"10.96.0.42"}
	if _, err := clientset.CoreV1().Services(ns).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to set ClusterIP on fake service: %v", err)
	}

	// 2. In-Place Update Pass with modified replicas and port
	updatedOpts := WorkloadOptions{
		OwnerUID: ownerUID,
		ImageTag: "v1.2.4",
		Parameters: map[string]string{
			"port":     "9091",
			"replicas": "5",
		},
		DrainTimeoutSeconds: 0, // Fall back to default 300s
	}

	if err := mgr.Ensure1PWorkload(ctx, ns, compName, updatedOpts); err != nil {
		t.Fatalf("Ensure1PWorkload in-place update failed: %v", err)
	}

	updatedDep, err := clientset.AppsV1().Deployments(ns).Get(ctx, compName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated Deployment: %v", err)
	}
	if *updatedDep.Spec.Replicas != 5 {
		t.Errorf("expected 5 replicas after update, got %d", *updatedDep.Spec.Replicas)
	}
	if *updatedDep.Spec.Template.Spec.TerminationGracePeriodSeconds != defaultDrainSeconds {
		t.Errorf("expected default drain seconds %d, got %d", defaultDrainSeconds, *updatedDep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}

	updatedSvc, err := clientset.CoreV1().Services(ns).Get(ctx, compName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated Service: %v", err)
	}
	if updatedSvc.Spec.Ports[0].Port != 9091 {
		t.Errorf("expected updated service port 9091, got %d", updatedSvc.Spec.Ports[0].Port)
	}
	// Verify ClusterIP was preserved
	if updatedSvc.Spec.ClusterIP != "10.96.0.42" {
		t.Errorf("expected preserved ClusterIP 10.96.0.42, got %q", updatedSvc.Spec.ClusterIP)
	}
}

func TestResolveImageAndParameters(t *testing.T) {
	t.Run("explicit full image override", func(t *testing.T) {
		opts := WorkloadOptions{
			Parameters: map[string]string{
				"image": "custom.registry.io/org/custom-img:tag-abc",
			},
		}
		img := resolveImage("test-comp", opts)
		if img != "custom.registry.io/org/custom-img:tag-abc" {
			t.Errorf("expected custom image, got %q", img)
		}
	})

	t.Run("imageRepository override with global.imageTag", func(t *testing.T) {
		opts := WorkloadOptions{
			Parameters: map[string]string{
				"imageRepository": "gcr.io/my-proj/ingestion",
				"global.imageTag": "v2.0.0",
			},
		}
		img := resolveImage("test-comp", opts)
		if img != "gcr.io/my-proj/ingestion:v2.0.0" {
			t.Errorf("expected repo+tag, got %q", img)
		}
	})

	t.Run("imageTag override in parameters", func(t *testing.T) {
		opts := WorkloadOptions{
			Parameters: map[string]string{
				"imageTag": "param-tag",
			},
		}
		img := resolveImage("my-comp", opts)
		expected := fmt.Sprintf("%s/my-comp:param-tag", defaultImageRepoPrefix)
		if img != expected {
			t.Errorf("expected %q, got %q", expected, img)
		}
	})

	t.Run("default image fallback", func(t *testing.T) {
		opts := WorkloadOptions{}
		img := resolveImage("my-comp", opts)
		expected := fmt.Sprintf("%s/my-comp:latest", defaultImageRepoPrefix)
		if img != expected {
			t.Errorf("expected %q, got %q", expected, img)
		}
	})
}

func TestDelete1PWorkload(t *testing.T) {
	t.Run("successfully deletes deployment and service", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, err := NewK8sWorkloadManager(clientset)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err = mgr.Ensure1PWorkload(context.Background(), "test-ns", "to-delete", WorkloadOptions{})
		if err != nil {
			t.Fatalf("Ensure1PWorkload() failed: %v", err)
		}

		err = mgr.Delete1PWorkload(context.Background(), "test-ns", "to-delete")
		if err != nil {
			t.Fatalf("Delete1PWorkload() failed: %v", err)
		}

		_, err = clientset.AppsV1().Deployments("test-ns").Get(context.Background(), "to-delete", metav1.GetOptions{})
		if err == nil {
			t.Fatal("expected deployment to be deleted")
		}

		_, err = clientset.CoreV1().Services("test-ns").Get(context.Background(), "to-delete", metav1.GetOptions{})
		if err == nil {
			t.Fatal("expected service to be deleted")
		}
	})

	t.Run("non-existent workload delete returns no error", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, err := NewK8sWorkloadManager(clientset)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err = mgr.Delete1PWorkload(context.Background(), "test-ns", "non-existent")
		if err != nil {
			t.Fatalf("Delete1PWorkload() failed: %v", err)
		}
	})
}

func TestFakeWorkloadManager(t *testing.T) {
	fakeMgr := NewFakeWorkloadManager()

	if fakeMgr.IsWorkloadActive("comp-a") {
		t.Fatal("expected comp-a to be inactive initially")
	}

	err := fakeMgr.Ensure1PWorkload(context.Background(), "default", "comp-a", WorkloadOptions{})
	if err != nil {
		t.Fatalf("Ensure1PWorkload() failed: %v", err)
	}

	if !fakeMgr.IsWorkloadActive("comp-a") {
		t.Fatal("expected comp-a to be active after Ensure1PWorkload")
	}

	err = fakeMgr.Delete1PWorkload(context.Background(), "default", "comp-a")
	if err != nil {
		t.Fatalf("Delete1PWorkload() failed: %v", err)
	}

	if fakeMgr.IsWorkloadActive("comp-a") {
		t.Fatal("expected comp-a to be inactive after Delete1PWorkload")
	}

	// Test Ensure error injection
	fakeMgr.EnsureErr = errors.New("injected ensure failure")
	err = fakeMgr.Ensure1PWorkload(context.Background(), "default", "comp-b", WorkloadOptions{})
	if err == nil || err.Error() != "injected ensure failure" {
		t.Fatalf("expected injected ensure error, got %v", err)
	}

	// Test Delete error injection
	fakeMgr.DeleteErr = errors.New("injected delete failure")
	err = fakeMgr.Delete1PWorkload(context.Background(), "default", "comp-a")
	if err == nil || err.Error() != "injected delete failure" {
		t.Fatalf("expected injected delete error, got %v", err)
	}

	// Test uninitialized FakeWorkloadManager struct literal
	uninitMgr := &FakeWorkloadManager{}
	if uninitMgr.IsWorkloadActive("comp-c") {
		t.Fatal("expected uninitialized manager to report inactive")
	}
	err = uninitMgr.Delete1PWorkload(context.Background(), "default", "comp-c")
	if err != nil {
		t.Fatalf("unexpected delete error on uninitialized manager: %v", err)
	}
	err = uninitMgr.Ensure1PWorkload(context.Background(), "default", "comp-c", WorkloadOptions{})
	if err != nil {
		t.Fatalf("unexpected ensure error on uninitialized manager: %v", err)
	}
	if !uninitMgr.IsWorkloadActive("comp-c") {
		t.Fatal("expected comp-c to be active after ensure on uninitialized manager")
	}
}

func TestFakeWorkloadManager_Concurrency(t *testing.T) {
	mgr := NewFakeWorkloadManager()
	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			comp := fmt.Sprintf("comp-%d", workerID)
			for j := 0; j < iterations; j++ {
				_ = mgr.Ensure1PWorkload(context.Background(), "default", comp, WorkloadOptions{})
				_ = mgr.IsWorkloadActive(comp)
				_ = mgr.Delete1PWorkload(context.Background(), "default", comp)
				_ = mgr.IsWorkloadActive(comp)
			}
		}(i)
	}
	wg.Wait()
}

func TestEnsure1PWorkload_ReconcileErrors(t *testing.T) {
	t.Run("deployment get failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("get", "deployments", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("get deployment db error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})
		if err == nil {
			t.Fatal("expected error on deployment get failure")
		}
	})

	t.Run("deployment create failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("create", "deployments", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("create deployment api error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})
		if err == nil {
			t.Fatal("expected error on deployment create failure")
		}
	})

	t.Run("deployment update failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, _ := NewK8sWorkloadManager(clientset)
		_ = mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})

		clientset.PrependReactor("update", "deployments", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("update deployment api error")
		})
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{ImageTag: "v2.0.0"})
		if err == nil {
			t.Fatal("expected error on deployment update failure")
		}
	})

	t.Run("service get failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("get", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("get service db error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})
		if err == nil {
			t.Fatal("expected error on service get failure")
		}
	})

	t.Run("service create failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("create", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("create service api error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})
		if err == nil {
			t.Fatal("expected error on service create failure")
		}
	})

	t.Run("service update failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, _ := NewK8sWorkloadManager(clientset)
		_ = mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{})

		clientset.PrependReactor("update", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("update service api error")
		})
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{
			Parameters: map[string]string{
				"port": "9090",
			},
		})
		if err == nil {
			t.Fatal("expected error on service update failure")
		}
	})

	t.Run("no-op update when options are unchanged", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{ImageTag: "v1.0.0"})
		if err != nil {
			t.Fatalf("unexpected error on initial ensure: %v", err)
		}

		// Prepend update reactors that fail if update is called
		clientset.PrependReactor("update", "deployments", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("unexpected deployment update call")
		})
		clientset.PrependReactor("update", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("unexpected service update call")
		})

		// Second ensure with identical options should be a no-op and NOT trigger update reactors
		err = mgr.Ensure1PWorkload(context.Background(), "default", "app", WorkloadOptions{ImageTag: "v1.0.0"})
		if err != nil {
			t.Fatalf("expected no-op ensure to succeed without calling update, got: %v", err)
		}
	})
}

func TestDelete1PWorkload_Errors(t *testing.T) {
	t.Run("deployment delete failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("delete", "deployments", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("delete deployment server error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Delete1PWorkload(context.Background(), "default", "app")
		if err == nil {
			t.Fatal("expected error on deployment delete failure")
		}
	})

	t.Run("service delete failure", func(t *testing.T) {
		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("delete", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("delete service server error")
		})
		mgr, _ := NewK8sWorkloadManager(clientset)
		err := mgr.Delete1PWorkload(context.Background(), "default", "app")
		if err == nil {
			t.Fatal("expected error on service delete failure")
		}
	})
}

func TestToSnakeCase_PunctuationAndFormatting(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "k8s-worker-pool", want: "k8s_worker_pool"},
		{input: "cluster.domain.name", want: "cluster_domain_name"},
		{input: "grpcPort", want: "grpc_port"},
		{input: "Already_Snake", want: "already_snake"},
		{input: "simple", want: "simple"},
		{input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildOwnerReferences_Empty(t *testing.T) {
	refs := buildOwnerReferences("test-comp", "")
	if refs != nil {
		t.Errorf("expected nil owner references for empty UID, got %+v", refs)
	}
}

func TestBuildArgs_Empty(t *testing.T) {
	if args := buildArgs(nil); args != nil {
		t.Errorf("expected nil args for nil params, got %+v", args)
	}
	onlySpecial := map[string]string{
		"image":    "foo",
		"replicas": "1",
		"port":     "8080",
	}
	if args := buildArgs(onlySpecial); args != nil {
		t.Errorf("expected nil args when all keys are special, got %+v", args)
	}
}

func TestEnsure1PWorkload_DetailedMutations(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	ns := "test-ns"
	comp := "mutated-comp"

	// Pre-create Deployment with empty containers and old labels/ownerRefs
	oldDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comp,
			Namespace: ns,
			Labels: map[string]string{
				"old-label": "old-val",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "OldOwner",
					Name:       "old",
					UID:        "old-uid",
				},
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{}, // 0 containers
				},
			},
		},
	}
	if _, err := clientset.AppsV1().Deployments(ns).Create(context.Background(), oldDep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create old deployment: %v", err)
	}

	// Pre-create Service with NodePort, old labels, old ownerRefs, old selector, old ports
	oldSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comp,
			Namespace: ns,
			Labels: map[string]string{
				"old-label": "old-val",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "OldOwner",
					Name:       "old",
					UID:        "old-uid",
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Selector: map[string]string{
				"old-app": "old-val",
			},
			Ports: []corev1.ServicePort{
				{
					Port: 7777,
				},
			},
		},
	}
	if _, err := clientset.CoreV1().Services(ns).Create(context.Background(), oldSvc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create old service: %v", err)
	}

	mgr, err := NewK8sWorkloadManager(clientset)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	opts := WorkloadOptions{
		OwnerUID: "new-owner-uid",
		ImageTag: "v3.0.0",
		Parameters: map[string]string{
			"port": "8080",
		},
	}

	if err := mgr.Ensure1PWorkload(context.Background(), ns, comp, opts); err != nil {
		t.Fatalf("Ensure1PWorkload failed: %v", err)
	}

	updatedDep, err := clientset.AppsV1().Deployments(ns).Get(context.Background(), comp, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}
	if len(updatedDep.Spec.Template.Spec.Containers) != 1 {
		t.Errorf("expected 1 container after mutation, got %d", len(updatedDep.Spec.Template.Spec.Containers))
	}
	if len(updatedDep.OwnerReferences) != 1 || updatedDep.OwnerReferences[0].UID != "new-owner-uid" {
		t.Errorf("expected updated owner references, got %+v", updatedDep.OwnerReferences)
	}

	updatedSvc, err := clientset.CoreV1().Services(ns).Get(context.Background(), comp, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated service: %v", err)
	}
	if updatedSvc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("expected ClusterIP service type after mutation, got %v", updatedSvc.Spec.Type)
	}
	if updatedSvc.Spec.Selector["app"] != comp {
		t.Errorf("expected selector app=%s, got %+v", comp, updatedSvc.Spec.Selector)
	}
	if len(updatedSvc.OwnerReferences) != 1 || updatedSvc.OwnerReferences[0].UID != "new-owner-uid" {
		t.Errorf("expected updated owner references on service, got %+v", updatedSvc.OwnerReferences)
	}
}

func TestEqualIntPointers(t *testing.T) {
	v32a, v32b, v32c := int32(10), int32(10), int32(20)
	if !equalInt32Ptr(nil, nil) {
		t.Error("expected equalInt32Ptr(nil, nil) to be true")
	}
	if equalInt32Ptr(&v32a, nil) || equalInt32Ptr(nil, &v32a) {
		t.Error("expected equalInt32Ptr with one nil to be false")
	}
	if !equalInt32Ptr(&v32a, &v32b) {
		t.Error("expected equalInt32Ptr with identical values to be true")
	}
	if equalInt32Ptr(&v32a, &v32c) {
		t.Error("expected equalInt32Ptr with different values to be false")
	}

	v64a, v64b, v64c := int64(100), int64(100), int64(200)
	if !equalInt64Ptr(nil, nil) {
		t.Error("expected equalInt64Ptr(nil, nil) to be true")
	}
	if equalInt64Ptr(&v64a, nil) || equalInt64Ptr(nil, &v64a) {
		t.Error("expected equalInt64Ptr with one nil to be false")
	}
	if !equalInt64Ptr(&v64a, &v64b) {
		t.Error("expected equalInt64Ptr with identical values to be true")
	}
	if equalInt64Ptr(&v64a, &v64c) {
		t.Error("expected equalInt64Ptr with different values to be false")
	}
}

func TestEnsure1PWorkload_ContainerFieldMutations(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	mgr, _ := NewK8sWorkloadManager(clientset)
	ns := "test-ns"
	comp := "mut-test"

	opts := WorkloadOptions{
		Parameters: map[string]string{
			"port":     "8080",
			"logLevel": "info",
		},
	}
	if err := mgr.Ensure1PWorkload(context.Background(), ns, comp, opts); err != nil {
		t.Fatalf("initial Ensure1PWorkload failed: %v", err)
	}

	// 1. Mutate args
	opts.Parameters["logLevel"] = "debug"
	if err := mgr.Ensure1PWorkload(context.Background(), ns, comp, opts); err != nil {
		t.Fatalf("Ensure1PWorkload with arg mutation failed: %v", err)
	}
	dep, _ := clientset.AppsV1().Deployments(ns).Get(context.Background(), comp, metav1.GetOptions{})
	if len(dep.Spec.Template.Spec.Containers[0].Args) == 0 || dep.Spec.Template.Spec.Containers[0].Args[0] != "-log_level=debug" {
		t.Errorf("expected updated args, got %+v", dep.Spec.Template.Spec.Containers[0].Args)
	}

	// 2. Mutate image tag
	opts.ImageTag = "v2.0.0"
	if err := mgr.Ensure1PWorkload(context.Background(), ns, comp, opts); err != nil {
		t.Fatalf("Ensure1PWorkload with image mutation failed: %v", err)
	}
	dep, _ = clientset.AppsV1().Deployments(ns).Get(context.Background(), comp, metav1.GetOptions{})
	expectedImg := fmt.Sprintf("%s/%s:v2.0.0", defaultImageRepoPrefix, comp)
	if dep.Spec.Template.Spec.Containers[0].Image != expectedImg {
		t.Errorf("expected updated image %q, got %q", expectedImg, dep.Spec.Template.Spec.Containers[0].Image)
	}
}
