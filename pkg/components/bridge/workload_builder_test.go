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

	"k8s.io/apimachinery/pkg/types"
)

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

func TestBuildDeploymentAndService(t *testing.T) {
	opts := WorkloadOptions{
		OwnerUID: types.UID("test-uid-1234"),
		Parameters: map[string]string{
			"port":     "8088",
			"replicas": "2",
			"logLevel": "debug",
		},
	}

	dep, err := buildDeployment("test-ns", "test-comp", opts)
	if err != nil {
		t.Fatalf("failed to build deployment: %v", err)
	}
	if dep.Name != "test-comp" || dep.Namespace != "test-ns" {
		t.Errorf("unexpected deployment metadata: %s/%s", dep.Namespace, dep.Name)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", *dep.Spec.Replicas)
	}
	if len(dep.OwnerReferences) != 1 || dep.OwnerReferences[0].UID != "test-uid-1234" {
		t.Errorf("expected owner reference with UID test-uid-1234, got %+v", dep.OwnerReferences)
	}

	svc, err := buildService("test-ns", "test-comp", opts)
	if err != nil {
		t.Fatalf("failed to build service: %v", err)
	}
	if svc.Name != "test-comp" || svc.Namespace != "test-ns" {
		t.Errorf("unexpected service metadata: %s/%s", svc.Namespace, svc.Name)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8088 {
		t.Errorf("expected service port 8088, got %+v", svc.Spec.Ports)
	}
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
