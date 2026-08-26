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
	"flag"
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
		name        string
		args        []string
		wantErr     bool
		wantHelpErr bool
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
			name:    "environment variable fallback",
			args:    []string{},
			wantErr: false,
		},
		{
			name:        "help flag returns ErrHelp",
			args:        []string{"--help"},
			wantErr:     true,
			wantHelpErr: true,
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
			if tt.wantHelpErr && !errors.Is(err, flag.ErrHelp) {
				t.Errorf("ParseConfig() error = %v, want errors.Is(flag.ErrHelp)", err)
			}
			if err == nil && cfg == nil {
				t.Fatal("expected non-nil Config")
			}
		})
	}
}

func TestBuildKubeConfig(t *testing.T) {
	t.Run("explicit invalid path fails immediately", func(t *testing.T) {
		_, err := BuildKubeConfig("/nonexistent/kubeconfig/path")
		if err == nil {
			t.Error("expected error for nonexistent kubeconfig path")
		}
	})

	t.Run("empty path falls back without panicking", func(t *testing.T) {
		// Outside of cluster, this will return an error or default config, but should not panic.
		_, _ = BuildKubeConfig("")
	})
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

func TestParseConfig_EnvVars(t *testing.T) {
	t.Setenv("CMS_ADDRESS", "custom-cms:50051")
	t.Setenv("NAMESPACE", "custom-ns")
	t.Setenv("PROBE_INTERVAL", "15s")
	t.Setenv("PROBE_TIMEOUT", "8s")
	t.Setenv("KUBECONFIG", "/custom/kubeconfig")

	cfg, err := ParseConfig([]string{})
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}
	if cfg.CMSAddress != "custom-cms:50051" {
		t.Errorf("cfg.CMSAddress = %q, want custom-cms:50051", cfg.CMSAddress)
	}
	if cfg.Namespace != "custom-ns" {
		t.Errorf("cfg.Namespace = %q, want custom-ns", cfg.Namespace)
	}
	if cfg.ProbeInterval != 15*time.Second {
		t.Errorf("cfg.ProbeInterval = %v, want 15s", cfg.ProbeInterval)
	}
	if cfg.ProbeTimeout != 8*time.Second {
		t.Errorf("cfg.ProbeTimeout = %v, want 8s", cfg.ProbeTimeout)
	}
	if cfg.Kubeconfig != "/custom/kubeconfig" {
		t.Errorf("cfg.Kubeconfig = %q, want /custom/kubeconfig", cfg.Kubeconfig)
	}
}
