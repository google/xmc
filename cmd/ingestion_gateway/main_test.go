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

package main

import (
	"context"
	"os"
	"testing"

	"xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
	"xmanager_cloud/pkg/components/crreader/crreadertest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type countingGetter struct {
	calls int
}

func (g *countingGetter) GetComponent(context.Context, string, string) (*unstructured.Unstructured, error) {
	g.calls++
	return nil, context.DeadlineExceeded
}

type stubProducer struct{}

func (s *stubProducer) Produce(ctx context.Context, record *ingestiongateway.MetricRecord, promise func(*ingestiongateway.MetricRecord, error)) {
	if promise != nil {
		promise(record, nil)
	}
}

func (s *stubProducer) Close() {}

func TestStartHTTPServerWithProducer(t *testing.T) {
	mp := &stubProducer{}
	srv := startHTTPServer(context.Background(), "0", "xmc-metrics", nil, mp)
	defer srv.Close()

	if srv == nil {
		t.Fatalf("expected non-nil server from startHTTPServer")
	}
}

func isolateInvalidKubeconfig(t *testing.T) {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	badKubeconfig := t.TempDir() + "/bad-kubeconfig"
	if err := os.WriteFile(badKubeconfig, []byte(":::invalid:::"), 0600); err != nil {
		t.Fatalf("failed to write invalid kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", badKubeconfig)
}

func TestResolveComponentConfig(t *testing.T) {
	ctx := context.Background()

	// 1. Nil config is a safe no-op.
	resolveComponentConfig(ctx, nil)

	// 2. Pub/Sub mode skips getter initialization and Component CR lookup completely.
	t.Run("pubsub skips getter initialization", func(t *testing.T) {
		isolateInvalidKubeconfig(t)

		pubsubCfg := &ingestiongateway.Config{
			Port:       "8080",
			MessageBus: ingestiongateway.MessageBusPubSub,
			ProjectID:  "test-project",
			TopicName:  "pubsub-topic",
		}
		resolveComponentConfig(ctx, pubsubCfg)
		if pubsubCfg.TopicName != "pubsub-topic" {
			t.Fatalf("TopicName changed in Pub/Sub mode: %q", pubsubCfg.TopicName)
		}

		spy := &countingGetter{}
		resolveComponentConfig(ctx, pubsubCfg, spy)
		if spy.calls != 0 {
			t.Errorf("GetComponent calls in Pub/Sub mode = %d, want 0", spy.calls)
		}
	})

	// 3. Redpanda config resolves from ComponentGetter.
	redpandaCfg := &ingestiongateway.Config{
		Port:                "8080",
		MessageBus:          ingestiongateway.MessageBusRedpanda,
		RedpandaBrokers:     []string{"static:9092"},
		TopicName:           "static-topic",
		MinPartitions:       1,
		MinReplicas:         1,
		ComponentsNamespace: "xmc",
	}
	comp := &unstructured.Unstructured{
		Object: map[string]any{
			"spec": map[string]any{
				"enabled": true,
				"parameters": map[string]any{
					"brokerEndpoints": "dynamic-broker:9092",
					"defaultTopic":    "dynamic-topic",
				},
			},
		},
	}
	resolveComponentConfig(ctx, redpandaCfg, &crreadertest.FakeGetter{Component: comp})
	if len(redpandaCfg.RedpandaBrokers) != 1 || redpandaCfg.RedpandaBrokers[0] != "dynamic-broker:9092" {
		t.Errorf("RedpandaBrokers = %v, want [dynamic-broker:9092]", redpandaCfg.RedpandaBrokers)
	}
	if redpandaCfg.TopicName != "dynamic-topic" {
		t.Errorf("TopicName = %q, want dynamic-topic", redpandaCfg.TopicName)
	}

	// Whitespace-only MessageBus defaults to Redpanda and resolves Component CR.
	wsBusCfg := &ingestiongateway.Config{
		Port:                "8080",
		MessageBus:          "   ",
		RedpandaBrokers:     []string{"static:9092"},
		TopicName:           "static-topic",
		MinPartitions:       1,
		MinReplicas:         1,
		ComponentsNamespace: "xmc",
	}
	resolveComponentConfig(ctx, wsBusCfg, &crreadertest.FakeGetter{Component: comp})
	if wsBusCfg.TopicName != "dynamic-topic" {
		t.Errorf("whitespace MessageBus TopicName = %q, want dynamic-topic", wsBusCfg.TopicName)
	}

	// 4. Getter error logs warning and retains static config.
	fallbackCfg := &ingestiongateway.Config{
		Port:            "8080",
		MessageBus:      ingestiongateway.MessageBusRedpanda,
		RedpandaBrokers: []string{"static:9092"},
		TopicName:       "static-topic",
		MinPartitions:   1,
		MinReplicas:     1,
	}
	resolveComponentConfig(ctx, fallbackCfg, &crreadertest.FakeGetter{Err: context.DeadlineExceeded})
	if fallbackCfg.TopicName != "static-topic" {
		t.Errorf("TopicName = %q, want static-topic on fallback", fallbackCfg.TopicName)
	}

	t.Run("redpanda falls back to static config when kubeconfig is unavailable", func(t *testing.T) {
		isolateInvalidKubeconfig(t)
		noKubeCfg := &ingestiongateway.Config{
			Port:            "8080",
			MessageBus:      ingestiongateway.MessageBusRedpanda,
			RedpandaBrokers: []string{"static:9092"},
			TopicName:       "static-topic",
			MinPartitions:   1,
			MinReplicas:     1,
		}

		resolveComponentConfig(ctx, noKubeCfg)
		if len(noKubeCfg.RedpandaBrokers) != 1 || noKubeCfg.RedpandaBrokers[0] != "static:9092" {
			t.Errorf("RedpandaBrokers = %v, want [static:9092] when kubeconfig is unavailable", noKubeCfg.RedpandaBrokers)
		}
		if noKubeCfg.TopicName != "static-topic" {
			t.Errorf("TopicName = %q, want static-topic when kubeconfig is unavailable", noKubeCfg.TopicName)
		}
	})
}
