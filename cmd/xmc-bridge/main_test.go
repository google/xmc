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
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

type testCMSClient struct{}

func (m *testCMSClient) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{Name: in.GetName(), Status: &componentspb.ComponentAccessStatus{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED}},
	}, nil
}

func (m *testCMSClient) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return &componentspb.UpdateObservedGenerationResponse{}, nil
}

func (m *testCMSClient) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{State: componentspb.ComponentState_COMPONENT_STATE_DELETED}, nil
}

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig([]string{})
	if err != nil || cfg.CMSAddress != "localhost:50051" || cfg.Namespace != "xmc" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}

	cfg, err = ParseConfig([]string{"--cms-address=10.0.0.1:90", "--namespace=ns", "--probe-interval=10s", "--probe-timeout=4s", "--kubeconfig=/tmp/k"})
	if err != nil || cfg.CMSAddress != "10.0.0.1:90" || cfg.Namespace != "ns" || cfg.ProbeInterval != 10*time.Second || cfg.ProbeTimeout != 4*time.Second || cfg.Kubeconfig != "/tmp/k" {
		t.Fatalf("unexpected custom config: %+v", cfg)
	}

	if _, err := ParseConfig([]string{"--bad-flag=true"}); err == nil {
		t.Error("expected error for bad flag")
	}
}

func TestBuildKubeConfig(t *testing.T) {
	if _, err := BuildKubeConfig("/invalid/path"); err == nil {
		t.Error("expected error for invalid kubeconfig path")
	}
	_, _ = BuildKubeConfig("")
}

func TestBuildStatusMap(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"K": "V"}},
		Conditions: []bridge.Condition{
			{Type: bridge.ConditionTypeReady, Status: bridge.ConditionStatusTrue, ObservedGeneration: 5, LastTransitionTime: time.Now().UTC()},
		},
	}
	s := BuildStatusMap(res)
	if s["observedGeneration"] != int64(5) || s["state"] != "COMPONENT_STATE_ENABLED" {
		t.Errorf("unexpected status map: %+v", s)
	}
}

func TestController_RunReconcilePass(t *testing.T) {
	scheme := runtime.NewScheme()
	active := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
			"metadata": map[string]any{"name": "clickhouse", "namespace": "xmc", "generation": int64(1)},
			"spec":     map[string]any{"enabled": true, "deletionProtection": true},
		},
	}
	deleting := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
			"metadata": map[string]any{"name": "redis", "namespace": "xmc", "generation": int64(2), "deletionTimestamp": "2026-08-24T00:00:00Z", "finalizers": []any{bridge.FinalizerDeletionProtection}},
			"spec":     map[string]any{"enabled": true, "deletionProtection": false},
		},
	}

	fakeDyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{componentGVR: "ComponentList"}, active, deleting)
	r, _ := bridge.NewReconciler(&testCMSClient{}, nil, nil)
	ctrl := NewController(fakeDyn, r, "xmc", 20*time.Millisecond)

	if err := ctrl.RunReconcilePass(context.Background()); err != nil {
		t.Fatalf("RunReconcilePass failed: %v", err)
	}

	act, _ := fakeDyn.Resource(componentGVR).Namespace("xmc").Get(context.Background(), "clickhouse", metav1.GetOptions{})
	if len(act.GetFinalizers()) != 1 {
		t.Errorf("expected finalizer added: %+v", act.GetFinalizers())
	}

	del, _ := fakeDyn.Resource(componentGVR).Namespace("xmc").Get(context.Background(), "redis", metav1.GetOptions{})
	if len(del.GetFinalizers()) != 0 {
		t.Errorf("expected finalizer removed: %+v", del.GetFinalizers())
	}
}

func TestController_Run_Shutdown(t *testing.T) {
	fakeDyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{componentGVR: "ComponentList"})
	r, _ := bridge.NewReconciler(&testCMSClient{}, nil, nil)
	ctrl := NewController(fakeDyn, r, "xmc", 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := ctrl.Run(ctx); err == nil || err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

type dummyServer struct {
	componentspb.UnimplementedComponentManagerServiceServer
}

func TestRunBridge(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	componentspb.RegisterComponentManagerServiceServer(srv, &dummyServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	tmp := t.TempDir()
	kPath := filepath.Join(tmp, "kc")
	_ = os.WriteFile(kPath, []byte("apiVersion: v1\nclusters:\n- cluster:\n    server: http://127.0.0.1:6443\n  name: c\ncontexts:\n- context:\n    cluster: c\n    user: u\n  name: x\ncurrent-context: x\nkind: Config\nusers:\n- name: u\n  user:\n    token: t\n"), 0600)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_ = RunBridge(ctx, &Config{CMSAddress: lis.Addr().String(), Namespace: "xmc", ProbeInterval: 10 * time.Millisecond, Kubeconfig: kPath})
	if err := RunBridge(ctx, &Config{Kubeconfig: "/invalid"}); err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}
