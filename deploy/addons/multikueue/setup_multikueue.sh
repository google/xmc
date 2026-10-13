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
MANIFESTS_DIR="${SCRIPT_DIR}/manifests"

PROJECT_ID="${PROJECT_ID:-xmc-dev}"
REGION="${REGION:-us-central1}"
# Must match the MultiKueueCluster resource name in manifests/management-multikueue.yaml.
readonly WORKER_CLUSTER_NAME="xmc-nightly-worker-1"
JOBSET_VERSION="${JOBSET_VERSION:-v0.12.0}"
KUEUE_VERSION="${KUEUE_VERSION:-v0.18.2}"
K3S_KUBECONFIG="/var/lib/rancher/k3s/k3s.yaml"

worker_kubectl() {
  kubectl exec -i -n kueue-system multikueue-worker-0 -- \
    kubectl --kubeconfig="${K3S_KUBECONFIG}" "$@"
}

echo "--> 1. Deploying in-cluster k3s MultiKueue worker StatefulSet..."
# Clean up legacy Deployment if upgrading from an older revision.
kubectl delete deployment multikueue-worker -n kueue-system --ignore-not-found
kubectl apply -f "${MANIFESTS_DIR}/multikueue-worker.yaml"
kubectl rollout status statefulset/multikueue-worker -n kueue-system --timeout=180s

echo "--> 2. Waiting for k3s worker node to become Ready..."
attempts=0
until [ -n "$(worker_kubectl get nodes -o name 2>/dev/null || true)" ]; do
  attempts=$((attempts + 1))
  if [ "${attempts}" -ge 90 ]; then
    echo "ERROR: Timed out after 180s waiting for k3s worker node to register." >&2
    exit 1
  fi
  sleep 2
done
worker_kubectl wait --for=condition=Ready node --all --timeout=180s

echo "--> 3. Installing JobSet (${JOBSET_VERSION}) and Kueue (${KUEUE_VERSION}) on k3s worker..."
worker_kubectl apply --server-side \
  -f "https://github.com/kubernetes-sigs/jobset/releases/download/${JOBSET_VERSION}/manifests.yaml" \
  -f "https://github.com/kubernetes-sigs/kueue/releases/download/${KUEUE_VERSION}/manifests.yaml"
worker_kubectl wait --for=condition=Available deployment/jobset-controller-manager -n jobset-system --timeout=180s
worker_kubectl wait --for=condition=Available deployment/kueue-controller-manager -n kueue-system --timeout=180s

echo "--> 4. Applying worker WorkloadPriorityClasses and queues on k3s worker..."
worker_kubectl apply -f - < "${MANIFESTS_DIR}/worker-queues.yaml"

echo "--> 5. Syncing worker kubeconfig Secret into management cluster..."
WORKER_KUBECONFIG="$(mktemp)"
trap 'rm -f "${WORKER_KUBECONFIG}"' EXIT
kubectl exec -n kueue-system multikueue-worker-0 -- cat "${K3S_KUBECONFIG}" \
  | sed 's|https://127.0.0.1:6443|https://multikueue-worker.kueue-system.svc:6443|g' \
  > "${WORKER_KUBECONFIG}"
kubectl -n kueue-system create secret generic multikueue-worker-kubeconfig \
  --from-file=kubeconfig="${WORKER_KUBECONFIG}" \
  --dry-run=client -o yaml | kubectl -n kueue-system apply -f -

echo "--> 6. Applying management cluster MultiKueue CRs and cluster metadata labels..."
kubectl apply -f "${MANIFESTS_DIR}/management-multikueue.yaml"
kubectl label multikueuecluster "${WORKER_CLUSTER_NAME}" \
  "xmc.google.com/project=${PROJECT_ID}" \
  "xmc.google.com/location=${REGION}" \
  "xmc.google.com/cluster-name=${WORKER_CLUSTER_NAME}" \
  --overwrite

echo "--> 7. Waiting for MultiKueueCluster and AdmissionCheck to become Active..."
kubectl wait --for=condition=Active "multikueuecluster/${WORKER_CLUSTER_NAME}" --timeout=180s
kubectl wait --for=condition=Active admissioncheck/multikueue-admission-check --timeout=180s
echo "✅ MultiKueue worker and queues are Active and ready."
