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
	"net"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"xmanager_cloud/pkg/clickhouse/componentconfig"
	"xmanager_cloud/pkg/components/crreader"
)

func TestClickHouse3PComponentE2E(t *testing.T) {
	harness := setupKindClusterHarness(t, clickhouseImage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns := createTestNamespace(ctx, t, harness, "xmc-ch-e2e")
	installThirdPartyCRD(ctx, t, harness, "clickhouse.altinity.com", "v1", "ClickHouseInstallation", "clickhouseinstallations")
	deployClickHouseInstance(ctx, t, harness, ns)

	host := fmt.Sprintf("clickhouse.%s.svc.cluster.local", ns)
	endpoint := fmt.Sprintf("%s:9000", host)
	applyThirdPartyStatusCR(ctx, t, harness, "clickhouse.altinity.com/v1", "ClickHouseInstallation", ns, "clickhouse-cluster", endpoint, clickhouseSecretName)

	modes := []struct {
		name, podName, marker string
		wrapperRef, params    map[string]any
	}{
		{
			name:       "OperatorManagedWrapperRef",
			wrapperRef: buildStandardWrapperRef("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "clickhouse-cluster"),
			params:     map[string]any{"defaultDatabase": "default", "user": "default", "secretPasswordKey": clickhouseSecretKey},
			podName:    "ch-workload-wrapper",
			marker:     "wrapper_ok",
		},
		{
			name: "OperatorlessStable2026aProfile",
			params: map[string]any{
				"host": host, "clusterEndpoints": endpoint, "nativePort": "9000", "httpPort": "8123",
				"defaultDatabase": "default", "user": "default", "secretName": clickhouseSecretName,
				"secretPasswordKey": clickhouseSecretKey, "storageSize": "10Gi",
				"healthCheckEndpoint": fmt.Sprintf("http://%s/ping", net.JoinHostPort(host, "8123")),
			},
			podName: "ch-workload-stable2026a",
			marker:  "stable2026a_ok",
		},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			applyThirdPartyComponentCR(ctx, t, harness, ns, "clickhouse", "analytical-db", "24.3.0", tc.wrapperRef, tc.params)
			envVars := waitForComponentReady(ctx, t, harness, ns, "clickhouse")
			assertComponentEnvVars(t, envVars, endpoint, host, "9000", clickhouseSecretName)
			assertClickHouseComponentResolver(ctx, t, harness, ns, endpoint)
			assertClickHouseWorkloadQuery(ctx, t, harness, ns, tc.podName, envVars, tc.marker)
		})
	}
}

func assertClickHouseComponentResolver(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, wantAddr string) {
	t.Helper()
	opts, err := componentconfig.ResolveOptions(ctx, componentconfig.Source{
		Enabled: true, Namespace: ns,
		Getter:  crreader.NewGetterFromDynamic(h.dynClient),
		Secrets: crreader.NewSecretReaderFromClientset(h.clientset),
	})
	if err != nil || opts == nil {
		t.Fatalf("componentconfig.ResolveOptions failed (opts=%v): %v", opts, err)
	}
	if len(opts.Addr) != 1 || opts.Addr[0] != wantAddr || opts.Auth.Database != "default" || opts.Auth.Username != "default" || opts.Auth.Password != clickhouseTestPass {
		t.Errorf("unexpected resolved ClickHouse options: Addr=%v Auth=%+v", opts.Addr, opts.Auth)
	}
}

func assertClickHouseWorkloadQuery(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, podName string, envVars map[string]string, marker string) {
	t.Helper()
	sqlCmd := fmt.Sprintf(
		`clickhouse-client --host "$HOST" --password "$CLICKHOUSE_PASSWORD" --multiquery --query `+
			`"CREATE TABLE IF NOT EXISTS e2e_metrics (id String) ENGINE = Memory; INSERT INTO e2e_metrics VALUES ('%s'); SELECT id FROM e2e_metrics WHERE id = '%s';"`,
		marker, marker,
	)
	logs := runMockMLWorkloadPod(ctx, t, h, workloadPodSpec{
		Namespace: ns, Name: podName, Image: clickhouseImage, Command: []string{"sh", "-c", sqlCmd}, AccessEnvVars: envVars,
		SecretEnvBinds: map[string]corev1.SecretKeySelector{
			"CLICKHOUSE_PASSWORD": {LocalObjectReference: corev1.LocalObjectReference{Name: envVars["SECRET_NAME"]}, Key: clickhouseSecretKey},
		},
	})
	if !strings.Contains(logs, marker) {
		t.Errorf("expected workload pod logs to contain query result %q, got:\n%s", marker, logs)
	}
}
