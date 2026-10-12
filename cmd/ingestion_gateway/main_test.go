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
	"testing"

	"github.com/google/xmc/cmd/ingestion_gateway/ingestiongateway"
	"github.com/google/xmc/pkg/components/crreader/crreadertest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

func TestResolveComponentConfigIfEnabled(t *testing.T) {
	ctx := context.Background()

	// 1. Nil or disabled config is a safe no-op.
	resolveComponentConfigIfEnabled(ctx, nil)
	disabledCfg := &ingestiongateway.Config{
		Port:             "8080",
		MessageBus:       ingestiongateway.MessageBusRedpanda,
		RedpandaBrokers:  []string{"static:9092"},
		TopicName:        "static-topic",
		MinPartitions:    1,
		MinReplicas:      1,
		EnableComponents: false,
	}
	resolveComponentConfigIfEnabled(ctx, disabledCfg)
	if disabledCfg.TopicName != "static-topic" {
		t.Fatalf("TopicName changed when EnableComponents=false: %q", disabledCfg.TopicName)
	}

	// 2. Enabled config resolves from ComponentGetter.
	enabledCfg := &ingestiongateway.Config{
		Port:                "8080",
		MessageBus:          ingestiongateway.MessageBusRedpanda,
		RedpandaBrokers:     []string{"static:9092"},
		TopicName:           "static-topic",
		MinPartitions:       1,
		MinReplicas:         1,
		EnableComponents:    true,
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
	resolveComponentConfigIfEnabled(ctx, enabledCfg, &crreadertest.FakeGetter{Component: comp})
	if len(enabledCfg.RedpandaBrokers) != 1 || enabledCfg.RedpandaBrokers[0] != "dynamic-broker:9092" {
		t.Errorf("RedpandaBrokers = %v, want [dynamic-broker:9092]", enabledCfg.RedpandaBrokers)
	}
	if enabledCfg.TopicName != "dynamic-topic" {
		t.Errorf("TopicName = %q, want dynamic-topic", enabledCfg.TopicName)
	}

	// 3. Getter error logs warning and retains static config.
	fallbackCfg := &ingestiongateway.Config{
		Port:             "8080",
		MessageBus:       ingestiongateway.MessageBusRedpanda,
		RedpandaBrokers:  []string{"static:9092"},
		TopicName:        "static-topic",
		MinPartitions:    1,
		MinReplicas:      1,
		EnableComponents: true,
	}
	resolveComponentConfigIfEnabled(ctx, fallbackCfg, &crreadertest.FakeGetter{Err: context.DeadlineExceeded})
	if fallbackCfg.TopicName != "static-topic" {
		t.Errorf("TopicName = %q, want static-topic on fallback", fallbackCfg.TopicName)
	}
}
