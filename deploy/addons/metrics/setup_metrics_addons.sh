#!/usr/bin/env bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
readonly XMC_NAMESPACE="xmc"
readonly METRICS_NAMESPACE="xmc-metrics"

echo "--> 1. Ensuring namespaces '${XMC_NAMESPACE}' and '${METRICS_NAMESPACE}' exist..."
kubectl create namespace "${XMC_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace "${METRICS_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

echo "--> 2. Creating ClickHouse format schema and init SQL ConfigMaps..."
kubectl create configmap clickhouse-format-schemas \
  --namespace="${METRICS_NAMESPACE}" \
  --from-file="metrics.proto=${REPO_ROOT}/api/protos/metrics/metrics.proto" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl create configmap clickhouse-init-scripts \
  --namespace="${METRICS_NAMESPACE}" \
  --from-file="init.sql=${REPO_ROOT}/deploy/clickhouse/schema.sql" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "--> 3. Deploying lightweight metrics addons (Redpanda and ClickHouse)..."
kubectl apply -f "${SCRIPT_DIR}/manifests/metrics-addons.yaml"

echo "--> 4. Waiting for Redpanda StatefulSet rollout in '${XMC_NAMESPACE}'..."
kubectl rollout status statefulset/redpanda -n "${XMC_NAMESPACE}" --timeout=180s

echo "--> 5. Initializing Kafka topic 'xmc-metrics' on redpanda-0..."
kubectl exec -n "${XMC_NAMESPACE}" redpanda-0 -- rpk topic create xmc-metrics --partitions 128 --replicas 1 || true

echo "--> 6. Waiting for ClickHouse StatefulSet rollout in '${METRICS_NAMESPACE}'..."
kubectl rollout status statefulset/clickhouse -n "${METRICS_NAMESPACE}" --timeout=180s

echo "--> 7. Applying ClickHouse schema from deploy/clickhouse/schema.sql..."
kubectl exec -i -n "${METRICS_NAMESPACE}" clickhouse-0 -- clickhouse-client --multiquery < "${REPO_ROOT}/deploy/clickhouse/schema.sql"

echo "--> 8. Restarting metrics-dependent deployments in '${XMC_NAMESPACE}' if present..."
for deploy in ingestion-gateway-deployment dashboard-service-deployment; do
  if kubectl get deployment "${deploy}" -n "${XMC_NAMESPACE}" &>/dev/null; then
    kubectl rollout restart "deployment/${deploy}" -n "${XMC_NAMESPACE}"
  fi
done

echo "✅ Metrics addons (ClickHouse & Redpanda) are configured and ready."
