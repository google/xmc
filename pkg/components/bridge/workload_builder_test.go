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

func TestResolveImage_FourTierPrecedence(t *testing.T) {
	tests := []struct {
		name          string
		componentName string
		opts          WorkloadOptions
		want          string
	}{
		{
			name:          "Tier 1: Explicit full image override takes top precedence",
			componentName: "ingestion-gateway",
			opts: WorkloadOptions{
				ImageTag: "ignored-tag",
				Parameters: map[string]string{
					"image":           "docker.io/my-org/custom-full-image:v1.9.9",
					"imageRepository": "gcr.io/ignored-repo",
					"imageTag":        "ignored-param-tag",
					"global.imageTag": "ignored-global-tag",
				},
			},
			want: "docker.io/my-org/custom-full-image:v1.9.9",
		},
		{
			name:          "Tier 2: Custom imageRepository with opts.ImageTag",
			componentName: "ingestion-gateway",
			opts: WorkloadOptions{
				ImageTag: "v2.5.0",
				Parameters: map[string]string{
					"imageRepository": "asia-docker.pkg.dev/my-proj/xmc-custom",
					"imageTag":        "fallback-tag",
					"global.imageTag": "global-tag",
				},
			},
			want: "asia-docker.pkg.dev/my-proj/xmc-custom:v2.5.0",
		},
		{
			name:          "Tier 2: Custom imageRepository with parameters.imageTag override",
			componentName: "query-engine",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"imageRepository": "gcr.io/custom-repo/query",
					"imageTag":        "feat-fast-query",
					"global.imageTag": "global-tag",
				},
			},
			want: "gcr.io/custom-repo/query:feat-fast-query",
		},
		{
			name:          "Tier 2: Custom imageRepository with global.imageTag override",
			componentName: "query-engine",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"imageRepository": "gcr.io/custom-repo/query",
					"global.imageTag": "global-v3.0.0",
				},
			},
			want: "gcr.io/custom-repo/query:global-v3.0.0",
		},
		{
			name:          "Tier 2: Custom imageRepository fallback to latest tag",
			componentName: "query-engine",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"imageRepository": "gcr.io/custom-repo/query",
				},
			},
			want: "gcr.io/custom-repo/query:latest",
		},
		{
			name:          "Tier 3: Default repository with opts.ImageTag override",
			componentName: "metadata-syncer",
			opts: WorkloadOptions{
				ImageTag: "v4.1.2",
			},
			want: fmt.Sprintf("%s/metadata-syncer:v4.1.2", defaultImageRepoPrefix),
		},
		{
			name:          "Tier 3: Default repository with parameters.imageTag override",
			componentName: "metadata-syncer",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"imageTag": "tag-from-params",
				},
			},
			want: fmt.Sprintf("%s/metadata-syncer:tag-from-params", defaultImageRepoPrefix),
		},
		{
			name:          "Tier 3: Default repository with global.imageTag override",
			componentName: "metadata-syncer",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"global.imageTag": "tag-from-global",
				},
			},
			want: fmt.Sprintf("%s/metadata-syncer:tag-from-global", defaultImageRepoPrefix),
		},
		{
			name:          "Tier 4: Default fallback with no tag or repo overrides",
			componentName: "metadata-syncer",
			opts:          WorkloadOptions{},
			want:          fmt.Sprintf("%s/metadata-syncer:latest", defaultImageRepoPrefix),
		},
		{
			name:          "Tier 4: Default fallback with empty parameters",
			componentName: "metadata-syncer",
			opts: WorkloadOptions{
				Parameters: map[string]string{
					"port": "8080",
				},
			},
			want: fmt.Sprintf("%s/metadata-syncer:latest", defaultImageRepoPrefix),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveImage(tt.componentName, tt.opts)
			if got != tt.want {
				t.Errorf("resolveImage(%q, %+v) = %q, want %q", tt.componentName, tt.opts, got, tt.want)
			}
		})
	}
}

func TestResolveTerminationGracePeriodSeconds(t *testing.T) {
	tests := []struct {
		name string
		opts WorkloadOptions
		want int64
	}{
		{
			name: "zero value falls back to default 300s",
			opts: WorkloadOptions{DrainTimeoutSeconds: 0},
			want: 300,
		},
		{
			name: "negative value falls back to default 300s",
			opts: WorkloadOptions{DrainTimeoutSeconds: -15},
			want: 300,
		},
		{
			name: "below minimum clamped to 5s for preStop execution",
			opts: WorkloadOptions{DrainTimeoutSeconds: 2},
			want: 5,
		},
		{
			name: "1 second clamped to 5s",
			opts: WorkloadOptions{DrainTimeoutSeconds: 1},
			want: 5,
		},
		{
			name: "exact minimum 5s preserved",
			opts: WorkloadOptions{DrainTimeoutSeconds: 5},
			want: 5,
		},
		{
			name: "custom timeout 60s preserved",
			opts: WorkloadOptions{DrainTimeoutSeconds: 60},
			want: 60,
		},
		{
			name: "custom timeout 120s preserved",
			opts: WorkloadOptions{DrainTimeoutSeconds: 120},
			want: 120,
		},
		{
			name: "custom timeout 600s preserved",
			opts: WorkloadOptions{DrainTimeoutSeconds: 600},
			want: 600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTerminationGracePeriodSeconds(tt.opts)
			if got != tt.want {
				t.Errorf("resolveTerminationGracePeriodSeconds(%+v) = %d, want %d", tt.opts, got, tt.want)
			}
		})
	}
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
