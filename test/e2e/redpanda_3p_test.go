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

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
	"xmanager_cloud/pkg/components/crreader"
)

const (
	redpandaImage      = "docker.redpanda.com/redpandadata/redpanda:v24.1.1"
	redpandaSecretName = "redpanda-secret"
)

func TestRedpanda3PComponentE2E(t *testing.T) {
	harness := setupKindClusterHarness(t, redpandaImage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns := createTestNamespace(ctx, t, harness, "xmc-rp-e2e")
	installThirdPartyCRD(ctx, t, harness, "cluster.redpanda.com", "v1alpha2", "Cluster", "clusters")
	deployRedpandaInstance(ctx, t, harness, ns)

	host := fmt.Sprintf("redpanda.%s.svc.cluster.local", ns)
	endpoint := fmt.Sprintf("%s:9092", host)
	applyThirdPartyStatusCR(ctx, t, harness, "cluster.redpanda.com/v1alpha2", "Cluster", ns, "redpanda-cluster", endpoint, redpandaSecretName)

	modes := []struct {
		name, wantSecret, wantTopic, podName, message string
		wrapperRef, params                            map[string]any
	}{
		{
			name:       "OperatorManagedWrapperRef",
			wrapperRef: buildStandardWrapperRef("cluster.redpanda.com", "v1alpha2", "Cluster", "redpanda-cluster"),
			params:     map[string]any{"defaultTopic": "xmc-metrics-wrapper"},
			wantSecret: redpandaSecretName, wantTopic: "xmc-metrics-wrapper",
			podName: "rp-workload-wrapper", message: "wrapper_msg_ok",
		},
		{
			name: "OperatorlessStable2026aProfile",
			params: map[string]any{
				"brokerEndpoints": endpoint, "kafkaPort": "9092", "adminPort": "9644",
				"defaultTopic": "xmc-metrics", "storageSize": "10Gi",
			},
			wantSecret: "", wantTopic: "xmc-metrics",
			podName: "rp-workload-stable2026a", message: "stable2026a_msg_ok",
		},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			applyThirdPartyComponentCR(ctx, t, harness, ns, "redpanda", "event-stream", "24.1.1", tc.wrapperRef, tc.params)
			envVars := waitForComponentReady(ctx, t, harness, ns, "redpanda")
			assertComponentEnvVars(t, envVars, endpoint, host, "9092", tc.wantSecret)
			assertRedpandaComponentResolver(ctx, t, harness, ns, endpoint, tc.wantTopic)
			assertRedpandaWorkloadPubSub(ctx, t, harness, ns, tc.podName, envVars, tc.wantTopic, tc.message)
		})
	}
}

func deployRedpandaInstance(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string) {
	t.Helper()
	advHost := fmt.Sprintf("redpanda.%s.svc.cluster.local", ns)
	deploySingleContainerService(ctx, t, h, serviceInstanceSpec{
		Namespace: ns, AppName: "redpanda", SecretName: redpandaSecretName,
		SecretData: map[string]string{"sasl-password": "xmc-redpanda-e2e-pass"},
		Ports:      map[string]int32{"kafka": 9092, "admin": 9644},
		Container: corev1.Container{
			Name: "redpanda", Image: redpandaImage, ImagePullPolicy: corev1.PullIfNotPresent,
			Args: []string{
				"redpanda", "start", "--smp", "1", "--memory", "512M", "--overprovisioned", "--node-id", "0",
				"--kafka-addr", "PLAINTEXT://0.0.0.0:9092", "--advertise-kafka-addr", fmt.Sprintf("PLAINTEXT://%s:9092", advHost),
				"--rpc-addr", "0.0.0.0:33145", "--advertise-rpc-addr", fmt.Sprintf("%s:33145", advHost), "--check=false",
			},
			Ports: []corev1.ContainerPort{{Name: "kafka", ContainerPort: 9092}, {Name: "admin", ContainerPort: 9644}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(9092)}},
				InitialDelaySeconds: 2, PeriodSeconds: 2,
			},
		},
	})
}

func assertRedpandaComponentResolver(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, wantBroker, wantTopic string) {
	t.Helper()
	cfg := &ingestiongateway.Config{
		Port: "8080", MessageBus: ingestiongateway.MessageBusRedpanda, RedpandaBrokers: []string{"localhost:9092"},
		TopicName: "fallback-topic", MinPartitions: 1, MinReplicas: 1, ComponentsNamespace: ns,
	}
	if err := ingestiongateway.ResolveRedpandaComponentConfig(ctx, cfg, crreader.NewGetterFromDynamic(h.dynClient)); err != nil {
		t.Fatalf("ResolveRedpandaComponentConfig failed: %v", err)
	}
	if len(cfg.RedpandaBrokers) != 1 || cfg.RedpandaBrokers[0] != wantBroker || cfg.TopicName != wantTopic {
		t.Errorf("unexpected resolved Redpanda config: Brokers=%v Topic=%q", cfg.RedpandaBrokers, cfg.TopicName)
	}
}

func assertRedpandaWorkloadPubSub(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, podName string, envVars map[string]string, topic, message string) {
	t.Helper()
	rpkScript := fmt.Sprintf(
		`rpk topic create %s --brokers "$HOST:$PORT" || true; printf '%s\n' | rpk topic produce %s --brokers "$ENDPOINT"; rpk topic consume %s --brokers "$ENDPOINT" -n 1`,
		topic, message, topic, topic,
	)
	logs := runMockMLWorkloadPod(ctx, t, h, workloadPodSpec{
		Namespace: ns, Name: podName, Image: redpandaImage, Command: []string{"sh", "-c", rpkScript}, AccessEnvVars: envVars,
	})
	if !strings.Contains(logs, message) {
		t.Errorf("expected workload pod logs to contain consumed Kafka message %q, got:\n%s", message, logs)
	}
}
