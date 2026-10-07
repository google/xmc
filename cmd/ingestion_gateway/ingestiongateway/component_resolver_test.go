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
	"reflect"
	"testing"

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
			name: "no-op when enable_components is false",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: false,
			},
			getter:      &crreadertest.FakeGetter{Err: errors.New("should not be called")},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "resolves live status.access.envVars ENDPOINT with port and defaultTopic from Component CR",
			cfg: &Config{
				Port:                "8080",
				MessageBus:          MessageBusRedpanda,
				RedpandaBrokers:     []string{"static-broker:9092"},
				TopicName:           "static-topic",
				MinPartitions:       1,
				MinReplicas:         1,
				EnableComponents:    true,
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
				EnableComponents:    true,
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
				EnableComponents:    true,
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
				EnableComponents:    true,
				ComponentsNamespace: "xmc",
			},
			getter:      &crreadertest.FakeGetter{Err: errors.New("k8s api unavailable")},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "returns error when getter is nil and enable_components is true",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
			},
			getter:      nil,
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "no-op when message_bus is pubsub even if enable_components is true",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusPubSub,
				ProjectID:        "test-proj",
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				EnableComponents: true,
			},
			getter:      &crreadertest.FakeGetter{Err: errors.New("should not be called")},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "preserves static config when status.state is COMPONENT_STATE_DISABLED",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
			},
			getter:      &crreadertest.FakeGetter{Component: &unstructured.Unstructured{}},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
		},
		{
			name: "does not duplicate port when ENDPOINT already contains host:port",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
			name: "restores original brokers and topic when cfg.Validate fails",
			cfg: &Config{
				Port:             "invalid-port",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
			},
			getter:      &crreadertest.FakeGetter{Component: baselineRedpandaComponent()},
			wantBrokers: []string{"static-broker:9092"},
			wantTopic:   "static-topic",
			wantErr:     true,
		},
		{
			name: "handles malformed field types in Component CR and retains static config",
			cfg: &Config{
				Port:             "8080",
				MessageBus:       MessageBusRedpanda,
				RedpandaBrokers:  []string{"static-broker:9092"},
				TopicName:        "static-topic",
				MinPartitions:    1,
				MinReplicas:      1,
				EnableComponents: true,
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
			if !reflect.DeepEqual(tc.cfg.RedpandaBrokers, tc.wantBrokers) {
				t.Errorf("RedpandaBrokers = %v, want %v", tc.cfg.RedpandaBrokers, tc.wantBrokers)
			}
			if tc.cfg.TopicName != tc.wantTopic {
				t.Errorf("TopicName = %q, want %q", tc.cfg.TopicName, tc.wantTopic)
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
		EnableComponents:    true,
		ComponentsNamespace: "xmc",
	}

	if err := ResolveRedpandaComponentConfig(context.Background(), cfg, getter); err != nil {
		t.Fatalf("ResolveRedpandaComponentConfig() failed: %v", err)
	}
	wantBrokers := []string{"redpanda-live.xmc.svc.cluster.local:9092"}
	if !reflect.DeepEqual(cfg.RedpandaBrokers, wantBrokers) {
		t.Errorf("RedpandaBrokers = %v, want %v", cfg.RedpandaBrokers, wantBrokers)
	}
	if cfg.TopicName != "xmc-metrics-from-component" {
		t.Errorf("TopicName = %q, want %q", cfg.TopicName, "xmc-metrics-from-component")
	}
}

func TestEnsureEndpointPort(t *testing.T) {
	t.Parallel()
	if got := ensureEndpointPort("redpanda-0.redpanda.xmc.svc.cluster.local", ""); got != "redpanda-0.redpanda.xmc.svc.cluster.local" {
		t.Errorf("ensureEndpointPort with empty port = %q", got)
	}
	if got := ensureEndpointPort("redpanda-0.redpanda.xmc.svc.cluster.local", "9092"); got != "redpanda-0.redpanda.xmc.svc.cluster.local:9092" {
		t.Errorf("ensureEndpointPort with 9092 = %q", got)
	}
	if got := ensureEndpointPort("redpanda-0.redpanda.xmc.svc.cluster.local:9092", "9093"); got != "redpanda-0.redpanda.xmc.svc.cluster.local:9092" {
		t.Errorf("ensureEndpointPort with existing port = %q", got)
	}
}
