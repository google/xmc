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
	"errors"
	"flag"
	"testing"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantHelpErr bool
	}{
		{
			name:    "valid flags",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-interval=10s", "--probe-timeout=5s"},
			wantErr: false,
		},
		{
			name:    "missing cms-address",
			args:    []string{"--namespace=prod", "--release-name=xmanager-cloud"},
			wantErr: true,
		},
		{
			name:    "missing namespace",
			args:    []string{"--cms-address=cms:50051", "--release-name=xmanager-cloud"},
			wantErr: true,
		},
		{
			name:    "missing release-name",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod"},
			wantErr: true,
		},
		{
			name:        "help flag returns ErrHelp",
			args:        []string{"--help"},
			wantErr:     true,
			wantHelpErr: true,
		},
		{
			name:    "invalid probe interval",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-interval=0s"},
			wantErr: true,
		},
		{
			name:    "invalid probe timeout",
			args:    []string{"--cms-address=cms:50051", "--namespace=prod", "--release-name=xmanager-cloud", "--probe-timeout=-1s"},
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

func TestBuildStatus(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	status := BuildStatus(res)
	if status.ObservedGeneration != 5 {
		t.Errorf("expected ObservedGeneration 5, got %v", status.ObservedGeneration)
	}
	if status.State != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected State COMPONENT_STATE_ENABLED, got %v", status.State)
	}
	if status.Access == nil || status.Access.EnvVars["PORT"] != "8080" {
		t.Errorf("expected Access.EnvVars[PORT]=8080, got %+v", status.Access)
	}
	if len(status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(status.Conditions))
	}
	cond := status.Conditions[0]
	if cond.Type != bridge.ConditionTypeReady || cond.Status != string(bridge.ConditionStatusTrue) || cond.ObservedGeneration != 5 || cond.Reason != "ComponentReady" || cond.Message != "All good" {
		t.Errorf("unexpected condition content: %+v", cond)
	}
}

func TestBuildStatusMap(t *testing.T) {
	res := &bridge.ReconcileResult{
		ObservedGeneration: 5,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Access:             &bridge.AccessStatus{EnvVars: map[string]string{"PORT": "8080"}},
		Conditions: []bridge.Condition{
			{
				Type:               bridge.ConditionTypeReady,
				Status:             bridge.ConditionStatusTrue,
				ObservedGeneration: 5,
				Reason:             "ComponentReady",
				Message:            "All good",
				LastTransitionTime: time.Now(),
			},
		},
	}

	statusMap := BuildStatusMap(res)
	if statusMap["observedGeneration"] != int64(5) {
		t.Errorf("expected observedGeneration 5, got %v", statusMap["observedGeneration"])
	}
	if statusMap["state"] != "COMPONENT_STATE_ENABLED" {
		t.Errorf("expected state COMPONENT_STATE_ENABLED, got %v", statusMap["state"])
	}

	accessMap, ok := statusMap["access"].(map[string]any)
	if !ok {
		t.Fatalf("expected access map in status, got %v", statusMap["access"])
	}
	envVars, ok := accessMap["envVars"].(map[string]any)
	if !ok || envVars["PORT"] != "8080" {
		t.Errorf("expected envVars[PORT]=8080, got %v", accessMap["envVars"])
	}

	conds, ok := statusMap["conditions"].([]any)
	if !ok || len(conds) != 1 {
		t.Fatalf("expected 1 condition in status, got %v", statusMap["conditions"])
	}
	condMap, ok := conds[0].(map[string]any)
	if !ok || condMap["type"] != bridge.ConditionTypeReady || condMap["status"] != string(bridge.ConditionStatusTrue) {
		t.Errorf("unexpected condition content: %+v", condMap)
	}
}

func TestParseConfig_EnvVars(t *testing.T) {
	t.Setenv("CMS_ADDRESS", "custom-cms:50051")
	t.Setenv("NAMESPACE", "custom-ns")
	t.Setenv("HELM_RELEASE_NAME", "custom-release")
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
	if cfg.ReleaseName != "custom-release" {
		t.Errorf("cfg.ReleaseName = %q, want custom-release", cfg.ReleaseName)
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

func TestParseConfig_InvalidEnvDuration(t *testing.T) {
	t.Setenv("CMS_ADDRESS", "cms:50051")
	t.Setenv("NAMESPACE", "xmc")
	t.Setenv("HELM_RELEASE_NAME", "xmanager-cloud")

	t.Run("invalid PROBE_INTERVAL", func(t *testing.T) {
		t.Setenv("PROBE_INTERVAL", "invalid-duration")
		_, err := ParseConfig([]string{})
		if err == nil {
			t.Error("expected error for invalid PROBE_INTERVAL, got nil")
		}
	})

	t.Run("invalid PROBE_TIMEOUT", func(t *testing.T) {
		t.Setenv("PROBE_TIMEOUT", "not-a-duration")
		_, err := ParseConfig([]string{})
		if err == nil {
			t.Error("expected error for invalid PROBE_TIMEOUT, got nil")
		}
	})
}
