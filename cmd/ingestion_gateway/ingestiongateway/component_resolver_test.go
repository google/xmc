// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ingestiongateway

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
)

func baselineRedpandaComponent() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      "redpanda",
				"namespace": "xmc",
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"parameters": map[string]any{
					"brokerEndpoints": "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
					"kafkaPort":       "9092",
					"defaultTopic":    "xmc-metrics-from-component",
				},
			},
			"status": map[string]any{
				"state": "COMPONENT_STATE_ENABLED",
				"access": map[string]any{
					"envVars": map[string]any{
						"ENDPOINT": "redpanda-live.xmc.svc.cluster.local",
						"PORT":     "9092",
					},
				},
			},
		},
	}
}

func TestResolveRedpandaComponentConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         *Config
		getter      ComponentGetter
		wantBrokers []string
		wantTopic   string
		wantErr     bool
	}{
		{
			name: "resolves live status.access.envVars ENDPOINT with port and defaultTopic from Component CR",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter:      &crreadertest.FakeGetter{Component: baselineRedpandaComponent()},
			wantBrokers: []string{"redpanda-live.xmc.svc.cluster.local:9092"},
			wantTopic:   "xmc-metrics-from-component",
		},
		{
			name: "falls back to spec.parameters.brokerEndpoints when status.access.envVars ENDPOINT is absent",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				delete(u.Object, "status")
				return u
			}()},
			wantBrokers: []string{"redpanda-0.redpanda.xmc.svc.cluster.local:9092"},
			wantTopic:   "xmc-metrics-from-component",
		},
		{
			name: "preserves static config when redpanda Component CR is disabled",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(u.Object, false, "spec", "enabled")
				return u
			}()},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "returns error when getter fails",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter:      &crreadertest.FakeGetter{Err: errors.New("k8s api unavailable")},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "returns error when getter is nil",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter:      nil,
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "no-op when message_bus is pubsub",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusPubSub,
				ProjectID:       "test-proj",
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
			},
			getter:      &crreadertest.FakeGetter{Err: errors.New("should not be called")},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "preserves static config when status.state is COMPONENT_STATE_DISABLED",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(u.Object, "COMPONENT_STATE_DISABLED", "status", "state")
				return u
			}()},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "preserves static config when Component CR is empty",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter:      &crreadertest.FakeGetter{Component: &unstructured.Unstructured{}},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "does not duplicate port when ENDPOINT already contains host:port",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(u.Object, "redpanda-live.xmc.svc.cluster.local:9093", "status", "access", "envVars", "ENDPOINT")
				return u
			}()},
			wantBrokers: []string{"redpanda-live.xmc.svc.cluster.local:9093"},
			wantTopic:   "xmc-metrics-from-component",
		},
		{
			name: "falls back to spec.parameters.kafkaPort when status.access.envVars.PORT is absent",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(u.Object, "", "status", "access", "envVars", "PORT")
				_ = unstructured.SetNestedField(u.Object, "9094", "spec", "parameters", "kafkaPort")
				return u
			}()},
			wantBrokers: []string{"redpanda-live.xmc.svc.cluster.local:9094"},
			wantTopic:   "xmc-metrics-from-component",
		},
		{
			name: "retains static brokers when Component CR has neither ENDPOINT nor brokerEndpoints",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				delete(u.Object, "status")
				_ = unstructured.SetNestedField(u.Object, "", "spec", "parameters", "brokerEndpoints")
				return u
			}()},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "xmc-metrics-from-component",
		},
		{
			name: "appends port to each broker in comma-separated status.access.envVars.ENDPOINT",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(
					u.Object,
					"redpanda-0.redpanda.xmc.svc.cluster.local, redpanda-1.redpanda.xmc.svc.cluster.local:9093",
					"status", "access", "envVars", "ENDPOINT",
				)
				return u
			}()},
			wantBrokers: []string{
				"redpanda-0.redpanda.xmc.svc.cluster.local:9092",
				"redpanda-1.redpanda.xmc.svc.cluster.local:9093",
			},
			wantTopic: "xmc-metrics-from-component",
		},
		{
			name: "preserves multi-broker spec.parameters.brokerEndpoints when status.access.envVars.ENDPOINT has only single probed broker",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(
					u.Object,
					"redpanda-0.redpanda.xmc.svc.cluster.local, redpanda-1.redpanda.xmc.svc.cluster.local:9095, redpanda-2.redpanda.xmc.svc.cluster.local",
					"spec", "parameters", "brokerEndpoints",
				)
				_ = unstructured.SetNestedField(u.Object, "9092", "spec", "parameters", "kafkaPort")
				_ = unstructured.SetNestedField(u.Object, "redpanda-0.redpanda.xmc.svc.cluster.local", "status", "access", "envVars", "ENDPOINT")
				_ = unstructured.SetNestedField(u.Object, "9999", "status", "access", "envVars", "PORT")
				return u
			}()},
			wantBrokers: []string{
				"redpanda-0.redpanda.xmc.svc.cluster.local:9092",
				"redpanda-1.redpanda.xmc.svc.cluster.local:9095",
				"redpanda-2.redpanda.xmc.svc.cluster.local:9092",
			},
			wantTopic: "xmc-metrics-from-component",
		},
		{
			name: "prefers multi-broker status.access.envVars.ENDPOINT over multi-broker spec.parameters.brokerEndpoints",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				ComponentsNamespace: "xmc",
			},
			getter: &crreadertest.FakeGetter{Component: func() *unstructured.Unstructured {
				u := baselineRedpandaComponent()
				_ = unstructured.SetNestedField(
					u.Object,
					"spec-0.redpanda.xmc.svc.cluster.local:9092,spec-1.redpanda.xmc.svc.cluster.local:9092",
					"spec", "parameters", "brokerEndpoints",
				)
				_ = unstructured.SetNestedField(
					u.Object,
					"live-0.redpanda.xmc.svc.cluster.local,live-1.redpanda.xmc.svc.cluster.local:9096",
					"status", "access", "envVars", "ENDPOINT",
				)
				_ = unstructured.SetNestedField(u.Object, "9094", "status", "access", "envVars", "PORT")
				return u
			}()},
			wantBrokers: []string{
				"live-0.redpanda.xmc.svc.cluster.local:9094",
				"live-1.redpanda.xmc.svc.cluster.local:9096",
			},
			wantTopic: "xmc-metrics-from-component",
		},
		{
			name: "restores original brokers and topic when cfg.Validate fails",
			cfg: &Config{
				Port:            "invalid-port",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter:      &crreadertest.FakeGetter{Component: baselineRedpandaComponent()},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "handles malformed field types in Component CR and retains static config",
			cfg: &Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"static-broker:9092"},
				TopicName:       "static-topic",
				MinPartitions:   1,
				MinReplicas:     1,
			},
			getter: &crreadertest.FakeGetter{Component: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "components.xmc.dev/v1alpha1",
					"kind":       "Component",
					"spec": map[string]any{
						"parameters": "invalid-not-a-map",
					},
					"status": map[string]any{
						"state": int64(42),
						"access": map[string]any{
							"envVars": "invalid-not-a-map",
						},
					},
				},
			}},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ResolveRedpandaComponentConfig(context.Background(), tc.cfg, tc.getter)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResolveRedpandaComponentConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantBrokers, tc.cfg.RedpandaBrokers); diff != "" {
				t.Errorf("RedpandaBrokers mismatch (-want +got):\n%s", diff)
			}
			if tc.cfg.TopicName != tc.wantTopic {
				t.Errorf("TopicName = %q, want %q", tc.cfg.TopicName, tc.wantTopic)
			}
		})
	}
}

func TestResolveBrokersFromComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		params      map[string]string
		envVars     map[string]string
		wantBrokers []string
		wantSource  string
	}{
		{
			name: "multi-broker ENDPOINT wins over multi-broker brokerEndpoints using PORT",
			params: map[string]string{
				"brokerEndpoints": "spec-0:9092,spec-1:9092,spec-2:9092",
				"kafkaPort":       "9092",
			},
			envVars: map[string]string{
				"ENDPOINT": "live-0, live-1:9095",
				"PORT":     "9093",
			},
			wantBrokers: []string{"live-0:9093", "live-1:9095"},
			wantSource:  "status.access.envVars.ENDPOINT",
		},
		{
			name: "multi-broker ENDPOINT falls back to spec kafkaPort when PORT is empty",
			params: map[string]string{
				"brokerEndpoints": "spec-0:9092",
				"kafkaPort":       "9094",
			},
			envVars: map[string]string{
				"ENDPOINT": "live-0, live-1",
			},
			wantBrokers: []string{"live-0:9094", "live-1:9094"},
			wantSource:  "status.access.envVars.ENDPOINT",
		},
		{
			name: "multi-broker brokerEndpoints wins over single-broker ENDPOINT and applies spec kafkaPort",
			params: map[string]string{
				"brokerEndpoints": "rp-0.redpanda.xmc.svc.cluster.local, rp-1.redpanda.xmc.svc.cluster.local:9095, rp-2.redpanda.xmc.svc.cluster.local",
				"kafkaPort":       "9092",
			},
			envVars: map[string]string{
				"ENDPOINT": "rp-0.redpanda.xmc.svc.cluster.local",
				"PORT":     "19092",
			},
			wantBrokers: []string{
				"rp-0.redpanda.xmc.svc.cluster.local:9092",
				"rp-1.redpanda.xmc.svc.cluster.local:9095",
				"rp-2.redpanda.xmc.svc.cluster.local:9092",
			},
			wantSource: "spec.parameters.brokerEndpoints",
		},
		{
			name: "trailing comma on single-broker ENDPOINT does not mask multi-broker brokerEndpoints",
			params: map[string]string{
				"brokerEndpoints": "rp-0:9092,rp-1:9092",
				"kafkaPort":       "9092",
			},
			envVars: map[string]string{
				"ENDPOINT": "rp-0,",
				"PORT":     "9092",
			},
			wantBrokers: []string{"rp-0:9092", "rp-1:9092"},
			wantSource:  "spec.parameters.brokerEndpoints",
		},
		{
			name: "single-broker ENDPOINT wins over single-broker brokerEndpoints",
			params: map[string]string{
				"brokerEndpoints": "spec-single:9092",
				"kafkaPort":       "9092",
			},
			envVars: map[string]string{
				"ENDPOINT": "live-single",
				"PORT":     "9093",
			},
			wantBrokers: []string{"live-single:9093"},
			wantSource:  "status.access.envVars.ENDPOINT",
		},
		{
			name: "single-broker brokerEndpoints fallback appends spec kafkaPort",
			params: map[string]string{
				"brokerEndpoints": "spec-single",
				"kafkaPort":       "9092",
			},
			envVars: map[string]string{
				"PORT": "19092",
			},
			wantBrokers: []string{"spec-single:9092"},
			wantSource:  "spec.parameters.brokerEndpoints",
		},
		{
			name:        "returns nil and empty source when both ENDPOINT and brokerEndpoints are empty",
			params:      map[string]string{"kafkaPort": "9092"},
			envVars:     map[string]string{"PORT": "9092", "ENDPOINT": " , "},
			wantBrokers: nil,
			wantSource:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBrokers, gotSource := resolveBrokersFromComponent(tc.params, tc.envVars)
			if diff := cmp.Diff(tc.wantBrokers, gotBrokers); diff != "" {
				t.Errorf("resolveBrokersFromComponent() brokers mismatch (-want +got):\n%s", diff)
			}
			if gotSource != tc.wantSource {
				t.Errorf("resolveBrokersFromComponent() source = %q, want %q", gotSource, tc.wantSource)
			}
		})
	}
}

func TestK8sComponentGetter_FakeDynamicClient(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme, baselineRedpandaComponent())
	getter := crreader.NewGetterFromDynamic(dynClient)

	cfg := &Config{
		Port:                "8080",
		MessageBus:          MessageBusRedpanda,
		RedpandaBrokers:     []string{"localhost:9092"},
		TopicName:           "xmc-metrics",
		MinPartitions:       1,
		MinReplicas:         1,
		ComponentsNamespace: "xmc",
	}

	if err := ResolveRedpandaComponentConfig(context.Background(), cfg, getter); err != nil {
		t.Fatalf("ResolveRedpandaComponentConfig() failed: %v", err)
	}
	wantBrokers := []string{"redpanda-live.xmc.svc.cluster.local:9092"}
	if diff := cmp.Diff(wantBrokers, cfg.RedpandaBrokers); diff != "" {
		t.Errorf("RedpandaBrokers mismatch (-want +got):\n%s", diff)
	}
	if cfg.TopicName != "xmc-metrics-from-component" {
		t.Errorf("TopicName = %q, want %q", cfg.TopicName, "xmc-metrics-from-component")
	}
}

func TestEnsureEndpointPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		port     string
		want     string
	}{
		{
			name:     "empty endpoint returns empty string",
			endpoint: "",
			port:     "9092",
			want:     "",
		},
		{
			name:     "whitespace endpoint returns empty string",
			endpoint: "   ",
			port:     "9092",
			want:     "",
		},
		{
			name:     "empty brackets endpoint returns empty string",
			endpoint: "[]",
			port:     "9092",
			want:     "",
		},
		{
			name:     "empty port returns endpoint unchanged",
			endpoint: "redpanda-0.redpanda.xmc.svc.cluster.local",
			port:     "",
			want:     "redpanda-0.redpanda.xmc.svc.cluster.local",
		},
		{
			name:     "appends port when endpoint has no port",
			endpoint: "redpanda-0.redpanda.xmc.svc.cluster.local",
			port:     "9092",
			want:     "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
		},
		{
			name:     "preserves existing port on endpoint",
			endpoint: "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
			port:     "9093",
			want:     "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
		},
		{
			name:     "formats bracketed IPv6 host without port",
			endpoint: "[::1]",
			port:     "9092",
			want:     "[::1]:9092",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ensureEndpointPort(tc.endpoint, tc.port)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ensureEndpointPort(%q, %q) mismatch (-want +got):\n%s", tc.endpoint, tc.port, diff)
			}
		})
	}
}
