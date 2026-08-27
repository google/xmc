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
	"reflect"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestBuildHelmValues(t *testing.T) {
	t.Run("basic 1p component with parameters and image tag", func(t *testing.T) {
		opts := HelmComponentOptions{
			ComponentName: "ingestion-gateway",
			Namespace:     "xmc",
			Enabled:       true,
			ImageTag:      "v1.2.3",
			Parameters: map[string]string{
				"minPartitions": "128",
				"tlsEnabled":    "true",
				"topicName":     "custom-metrics",
			},
		}

		values := BuildHelmValues(opts)
		if values == nil {
			t.Fatal("expected non-nil values map")
		}

		global, ok := values["global"].(map[string]any)
		if !ok || global["imageTag"] != "v1.2.3" {
			t.Errorf("expected global.imageTag = 'v1.2.3', got %+v", values["global"])
		}

		depValues, ok := values["ingestionGatewayDeployment"].(map[string]any)
		if !ok {
			t.Fatalf("expected ingestionGatewayDeployment key in values, got %+v", values)
		}

		if depValues["enabled"] != true {
			t.Errorf("expected enabled = true, got %v", depValues["enabled"])
		}
		if depValues["minPartitions"] != int64(128) {
			t.Errorf("expected minPartitions = 128, got %v (%T)", depValues["minPartitions"], depValues["minPartitions"])
		}
		if depValues["tlsEnabled"] != true {
			t.Errorf("expected tlsEnabled = true, got %v (%T)", depValues["tlsEnabled"], depValues["tlsEnabled"])
		}
		if depValues["topicName"] != "custom-metrics" {
			t.Errorf("expected topicName = 'custom-metrics', got %v", depValues["topicName"])
		}
	})

	t.Run("component name key mapping", func(t *testing.T) {
		tests := []struct {
			name    string
			wantKey string
		}{
			{"ingestion-gateway", "ingestionGatewayDeployment"},
			{"ingestion_gateway", "ingestionGatewayDeployment"},
			{"rod", "rodDeployment"},
			{"rod-data-provider", "rodDeployment"},
			{"health-service", "healthServiceDeployment"},
			{"metrics", "metrics"},
			{"custom-service", "customServiceDeployment"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := ComponentNameToHelmKey(tt.name); got != tt.wantKey {
					t.Errorf("ComponentNameToHelmKey(%q) = %q, want %q", tt.name, got, tt.wantKey)
				}
			})
		}
	})

	t.Run("raw values merging", func(t *testing.T) {
		opts := HelmComponentOptions{
			ComponentName: "custom-worker",
			Enabled:       true,
			RawValues: map[string]any{
				"resources": map[string]any{
					"limits": map[string]any{"cpu": "500m"},
				},
			},
		}

		values := BuildHelmValues(opts)
		compMap, ok := values["customWorkerDeployment"].(map[string]any)
		if !ok {
			t.Fatalf("expected customWorkerDeployment map in %+v", values)
		}
		if !reflect.DeepEqual(compMap["resources"], opts.RawValues["resources"]) {
			t.Errorf("expected resources map %+v, got %+v", opts.RawValues["resources"], compMap["resources"])
		}
	})
}

func TestParseHelmComponentOptions(t *testing.T) {
	spec := &componentspb.ComponentSpec{
		Version: "v2.0.0",
		LifecycleControl: &componentspb.LifecycleControl{
			DrainingTimeout: &durationpb.Duration{Seconds: 45},
		},
	}

	rawSpec := map[string]any{
		"releaseName": "custom-xmc-release",
		"imageTag":    "v2.0.0-override",
		"parameters": map[string]any{
			"port":     8080,
			"replicas": 3,
		},
		"values": map[string]any{
			"hpa": map[string]any{"enabled": true},
		},
	}

	opts := ParseHelmComponentOptions("ingestion-gateway", "xmc", rawSpec, spec)
	if opts.ComponentName != "ingestion-gateway" || opts.Namespace != "xmc" {
		t.Errorf("unexpected name/namespace in %+v", opts)
	}
	if opts.ReleaseName != "custom-xmc-release" {
		t.Errorf("expected releaseName 'custom-xmc-release', got %q", opts.ReleaseName)
	}
	if opts.ImageTag != "v2.0.0-override" {
		t.Errorf("expected imageTag 'v2.0.0-override', got %q", opts.ImageTag)
	}
	if opts.DrainTimeoutSeconds != 45 {
		t.Errorf("expected drainTimeoutSeconds 45, got %d", opts.DrainTimeoutSeconds)
	}
	if opts.Parameters["port"] != "8080" || opts.Parameters["replicas"] != "3" {
		t.Errorf("unexpected parameters in %+v", opts.Parameters)
	}
	if hpa, ok := opts.RawValues["hpa"].(map[string]any); !ok || hpa["enabled"] != true {
		t.Errorf("unexpected raw values in %+v", opts.RawValues)
	}

	emptyOpts := ParseHelmComponentOptions("test", "default", nil, nil)
	if emptyOpts.ComponentName != "test" || !emptyOpts.Enabled {
		t.Errorf("unexpected empty options: %+v", emptyOpts)
	}
}

func TestFakeHelmManager(t *testing.T) {
	ctx := context.Background()
	mgr := NewFakeHelmManager()

	opts := HelmComponentOptions{
		ComponentName: "ingestion-gateway",
		Namespace:     "xmc",
		Enabled:       true,
		ImageTag:      "v1.0.0",
	}

	if err := mgr.Ensure1PComponent(ctx, "xmc", "ingestion-gateway", opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if !mgr.IsComponentActive("ingestion-gateway") {
		t.Error("expected ingestion-gateway to be active")
	}

	if err := mgr.Delete1PComponent(ctx, "xmc", "ingestion-gateway"); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive("ingestion-gateway") {
		t.Error("expected ingestion-gateway to be inactive after delete")
	}

	// Test error injection
	mgr.EnsureErr = errors.New("ensure error")
	if err := mgr.Ensure1PComponent(ctx, "xmc", "ingestion-gateway", opts); err == nil {
		t.Fatal("expected error on EnsureErr injection")
	}

	mgr.DeleteErr = errors.New("delete error")
	if err := mgr.Delete1PComponent(ctx, "xmc", "ingestion-gateway"); err == nil {
		t.Fatal("expected error on DeleteErr injection")
	}

	// Test nil receiver
	var nilMgr *FakeHelmManager
	if err := nilMgr.Ensure1PComponent(ctx, "xmc", "ingestion-gateway", opts); err != nil {
		t.Errorf("expected nil error on nil receiver, got %v", err)
	}
	if err := nilMgr.Delete1PComponent(ctx, "xmc", "ingestion-gateway"); err != nil {
		t.Errorf("expected nil error on nil receiver, got %v", err)
	}
	if nilMgr.IsComponentActive("ingestion-gateway") {
		t.Error("expected false on nil receiver IsComponentActive")
	}
}
