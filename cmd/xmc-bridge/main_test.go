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

package main

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "default values",
			args:    []string{},
			wantErr: false,
		},
		{
			name:    "custom values",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--probe-interval=10s", "--probe-timeout=5s"},
			wantErr: false,
		},
		{
			name:    "invalid probe interval",
			args:    []string{"--probe-interval=0s"},
			wantErr: true,
		},
		{
			name:    "invalid probe timeout",
			args:    []string{"--probe-timeout=-1s"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg == nil {
				t.Fatal("expected non-nil Config")
			}
		})
	}
}

func TestBuildKubeConfig(t *testing.T) {
	_, err := BuildKubeConfig("/nonexistent/kubeconfig/path")
	if err == nil {
		t.Error("expected error for nonexistent kubeconfig path")
	}
}

func TestController_RunReconcilePass(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})

	compObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       "test-comp",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"spec": map[string]any{
				"enabled":            true,
				"deletionProtection": true,
			},
		},
	}

	_, err := fakeDynamic.Resource(componentGVR).Namespace("xmc").Create(ctx, compObj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create test resource: %v", err)
	}

	ctrl := NewController(fakeDynamic, "xmc", time.Second, time.Second)
	if err := ctrl.RunReconcilePass(ctx); err != nil {
		t.Fatalf("RunReconcilePass failed: %v", err)
	}
}

func TestController_Run_ContextCancel(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		componentGVR: "ComponentList",
	})

	ctrl := NewController(fakeDynamic, "xmc", 10*time.Millisecond, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- ctrl.Run(ctx)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	err := <-done
	if err == nil || err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}
