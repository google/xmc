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
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

type testCMSClient struct {
	mu sync.Mutex
}

func (t *testCMSClient) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{Name: in.GetName(), Spec: in.GetSpec()},
	}, nil
}

func (t *testCMSClient) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return &componentspb.UpdateObservedGenerationResponse{
		Component: &componentspb.Component{Name: in.GetName()},
	}, nil
}

func (t *testCMSClient) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	return &componentspb.CompleteDeactivationResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
	}, nil
}

func (t *testCMSClient) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{
		State: componentspb.ComponentState_COMPONENT_STATE_DELETED,
	}, nil
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCMS   string
		wantNS    string
		wantIntvl time.Duration
	}{
		{
			name:      "defaults",
			args:      []string{},
			wantCMS:   "experiment-state-server:50051",
			wantNS:    "xmc",
			wantIntvl: 15 * time.Second,
		},
		{
			name: "custom args",
			args: []string{
				"--cms-address=localhost:9090",
				"--namespace=prod-xmc",
				"--probe-interval=30s",
				"--probe-timeout=10s",
			},
			wantCMS:   "localhost:9090",
			wantNS:    "prod-xmc",
			wantIntvl: 30 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseFlags(tc.args)
			if err != nil {
				t.Fatalf("unexpected error parsing flags: %v", err)
			}
			if cfg.CMSAddress != tc.wantCMS {
				t.Errorf("CMSAddress = %s, want %s", cfg.CMSAddress, tc.wantCMS)
			}
			if cfg.Namespace != tc.wantNS {
				t.Errorf("Namespace = %s, want %s", cfg.Namespace, tc.wantNS)
			}
			if cfg.ProbeInterval != tc.wantIntvl {
				t.Errorf("ProbeInterval = %v, want %v", cfg.ProbeInterval, tc.wantIntvl)
			}
		})
	}
}

func TestBuildKubeConfigInvalidPath(t *testing.T) {
	_, err := BuildKubeConfig("/non/existent/path/kubeconfig")
	if err == nil {
		t.Fatal("expected error for non-existent kubeconfig path, got nil")
	}
}

func TestControllerReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			bridge.ComponentGVR: "ComponentList",
		},
	)
	cmsClient := &testCMSClient{}
	prober := bridge.New(fakeDynamic)

	reconciler, err := bridge.NewReconciler(cmsClient, prober, fakeDynamic)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	compObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":       "bridge-test-comp",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
				"version":       "v1.0.0",
			},
		},
	}

	_, err = fakeDynamic.Resource(bridge.ComponentGVR).Namespace("xmc").Create(context.Background(), compObj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create fake CR: %v", err)
	}

	ctrl := NewController(fakeDynamic, cmsClient, reconciler, "xmc", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := ctrl.ReconcileAll(ctx); err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	// Test controller Run with early context cancellation
	runErr := ctrl.Run(ctx)
	if runErr != nil && !errors.Is(runErr, context.DeadlineExceeded) && !errors.Is(runErr, context.Canceled) {
		t.Fatalf("expected context deadline exceeded or canceled error, got: %v", runErr)
	}
}
