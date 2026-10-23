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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ClickHouse server instance shared by the KinD suites that need a live ClickHouse service.
const (
	clickhouseImage      = "clickhouse/clickhouse-server:24.3-alpine"
	clickhouseSecretName = "clickhouse-secret"
	clickhouseSecretKey  = "password"
	clickhouseTestPass   = "xmc-clickhouse-e2e-pass"
)

// deployClickHouseInstance runs a single-replica ClickHouse server behind the NodePort Service
// "clickhouse" in ns, with its password in the Secret clickhouseSecretName, and waits until it is
// Ready.
func deployClickHouseInstance(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string) {
	t.Helper()
	passRef := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: clickhouseSecretName}, Key: clickhouseSecretKey}
	deploySingleContainerService(ctx, t, h, serviceInstanceSpec{
		Namespace: ns, AppName: "clickhouse", SecretName: clickhouseSecretName,
		SecretData: map[string]string{clickhouseSecretKey: clickhouseTestPass},
		Ports:      map[string]int32{"http": 8123, "tcp": 9000},
		Container: corev1.Container{
			Name: "clickhouse", Image: clickhouseImage, ImagePullPolicy: corev1.PullIfNotPresent,
			Env:   []corev1.EnvVar{{Name: "CLICKHOUSE_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: passRef}}},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8123}, {Name: "tcp", ContainerPort: 9000}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ping", Port: intstr.FromInt(8123)}},
				InitialDelaySeconds: 1, PeriodSeconds: 2,
			},
		},
	})
}
