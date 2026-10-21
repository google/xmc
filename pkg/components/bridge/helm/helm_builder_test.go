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

package helm

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestBuildHelmValues(t *testing.T) {
	t.Run("basic 1p component with parameters and image tag", func(t *testing.T) {
		opts := HelmComponentOptions{
			Ref: ComponentRef{
				Namespace: "xmc",
				Name:      "ingestion-gateway",
			},
			Enabled:             true,
			ImageTag:            "v1.2.3",
			DrainTimeoutSeconds: 30,
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

		if enabled, ok := depValues["enabled"].(bool); !ok || !enabled {
			t.Errorf("expected enabled = true, got %v", depValues["enabled"])
		}
		if depValues["drainTimeoutSeconds"] != int32(30) {
			t.Errorf("expected drainTimeoutSeconds = 30, got %v (%T)", depValues["drainTimeoutSeconds"], depValues["drainTimeoutSeconds"])
		}
		if depValues["minPartitions"] != int64(128) {
			t.Errorf("expected minPartitions = 128, got %v (%T)", depValues["minPartitions"], depValues["minPartitions"])
		}
		if tlsEnabled, ok := depValues["tlsEnabled"].(bool); !ok || !tlsEnabled {
			t.Errorf("expected tlsEnabled = true, got %v (%T)", depValues["tlsEnabled"], depValues["tlsEnabled"])
		}
		if depValues["topicName"] != "custom-metrics" {
			t.Errorf("expected topicName = 'custom-metrics', got %v", depValues["topicName"])
		}
	})

	t.Run("non-image parameters do not emit image key", func(t *testing.T) {
		opts := HelmComponentOptions{
			Ref: ComponentRef{
				Namespace: "xmc",
				Name:      "ingestion-gateway",
			},
			Enabled: true,
			Parameters: map[string]string{
				"minPartitions": "128",
				"tlsEnabled":    "true",
			},
		}

		values := BuildHelmValues(opts)
		depValues, ok := values["ingestionGatewayDeployment"].(map[string]any)
		if !ok {
			t.Fatalf("expected ingestionGatewayDeployment key in values, got %+v", values)
		}

		if _, exists := depValues["image"]; exists {
			t.Errorf("expected no 'image' key when only non-image parameters provided, but found: %v", depValues["image"])
		}
		if _, exists := values["global"]; exists {
			t.Errorf("expected no 'global' key when imageTag is empty, but found: %+v", values["global"])
		}
		if depValues["minPartitions"] != int64(128) {
			t.Errorf("expected minPartitions = 128, got %v", depValues["minPartitions"])
		}
	})

	t.Run("component name key mapping", func(t *testing.T) {
		tests := []struct {
			name    string
			wantKey string
		}{
			{"ingestion-gateway", "ingestionGatewayDeployment"},
			{"rod-data-provider", "rodDeployment"},
			{"health-service", "healthServiceDeployment"},
			{"metrics", "metrics"},
			{"xmc-bridge", "componentsDeployment"},
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
			Ref: ComponentRef{
				Name: "custom-worker",
			},
			Enabled: true,
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
	if opts.Ref.Name != "ingestion-gateway" || opts.Ref.Namespace != "xmc" {
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
	hpa, ok := opts.RawValues["hpa"].(map[string]any)
	enabled, _ := hpa["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("unexpected raw values in %+v", opts.RawValues)
	}

	emptyOpts := ParseHelmComponentOptions("test", "default", nil, nil)
	if emptyOpts.Ref.Name != "test" || !emptyOpts.Enabled {
		t.Errorf("unexpected empty options: %+v", emptyOpts)
	}

	chartPathOpts := ParseHelmComponentOptions("ingestion-gateway", "xmc", map[string]any{
		"parameters": map[string]any{
			"chartPath": "/charts/components/ingestion-gateway",
			"replicas":  2,
		},
	}, nil)
	if chartPathOpts.ChartPath != "/charts/components/ingestion-gateway" {
		t.Errorf("expected ChartPath '/charts/components/ingestion-gateway', got %q", chartPathOpts.ChartPath)
	}
	builtVals := BuildHelmValues(chartPathOpts)
	depVals, _ := builtVals["ingestionGatewayDeployment"].(map[string]any)
	if _, leaked := depVals["chartPath"]; leaked {
		t.Errorf("expected chartPath to be excluded from Helm deployment values, got %+v", depVals)
	}
}

func TestResolveImage(t *testing.T) {
	tests := []struct {
		name       string
		component  string
		imageTag   string
		parameters map[string]string
		wantImage  string
	}{
		{
			name:       "full image parameter override (tier 1)",
			component:  "ingestion-gateway",
			imageTag:   "v1.0.0",
			parameters: map[string]string{"image": "custom.registry.io/custom-image:custom-tag"},
			wantImage:  "custom.registry.io/custom-image:custom-tag",
		},
		{
			name:       "custom imageRepository + tag (tier 2)",
			component:  "ingestion-gateway",
			imageTag:   "v1.2.0",
			parameters: map[string]string{"imageRepository": "custom.registry.io/repo"},
			wantImage:  "custom.registry.io/repo:v1.2.0",
		},
		{
			name:       "default repository + tag override (tier 3)",
			component:  "ingestion-gateway",
			imageTag:   "v3.0.0",
			parameters: nil,
			wantImage:  "us-docker.pkg.dev/xmc-prod/xmc/ingestion-gateway:v3.0.0",
		},
		{
			name:       "default repository + latest (tier 4)",
			component:  "rod-data-provider",
			imageTag:   "",
			parameters: nil,
			wantImage:  "us-docker.pkg.dev/xmc-prod/xmc/rod-data-provider:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveImage(tt.component, tt.imageTag, tt.parameters)
			if got != tt.wantImage {
				t.Errorf("ResolveImage(%q, %q, %+v) = %q, want %q", tt.component, tt.imageTag, tt.parameters, got, tt.wantImage)
			}
		})
	}
}

func TestDefaultComponentRepository(t *testing.T) {
	tests := []struct {
		name      string
		component string
		wantRepo  string
	}{
		{
			name:      "ingestion gateway",
			component: "ingestion-gateway",
			wantRepo:  "us-docker.pkg.dev/xmc-prod/xmc/ingestion-gateway",
		},
		{
			name:      "rod data provider",
			component: "rod-data-provider",
			wantRepo:  "us-docker.pkg.dev/xmc-prod/xmc/rod-data-provider",
		},
		{
			name:      "health service",
			component: "health-service",
			wantRepo:  "us-docker.pkg.dev/xmc-prod/xmc/health-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultComponentRepository(tt.component); got != tt.wantRepo {
				t.Errorf("DefaultComponentRepository(%q) = %q, want %q", tt.component, got, tt.wantRepo)
			}
		})
	}
}
