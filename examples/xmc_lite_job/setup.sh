#!/usr/bin/env bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ENV_FILE="${ENV_FILE:-${REPO_ROOT}/.env}"

if [[ -f "${ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
fi

: "${PROJECT_ID:?Error: PROJECT_ID environment variable is required (set in .env or shell)}"
: "${GKE_CLUSTER:?Error: GKE_CLUSTER environment variable is required (set in .env or shell)}"
: "${GCP_REGION:?Error: GCP_REGION environment variable is required (set in .env or shell)}"

K8S_NAMESPACE="${K8S_NAMESPACE:-default}"
KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/config}"
export KUBECONFIG

PROVISION_CLUSTER="${PROVISION_CLUSTER:-false}"
if [[ "${1:-}" == "--provision-cluster" ]]; then
  PROVISION_CLUSTER="true"
fi

if [[ "${PROVISION_CLUSTER}" == "true" ]]; then
  : "${XMANAGER_ENV:?Error: XMANAGER_ENV environment variable is required to provision a cluster}"
  GCLUSTER_BIN="${GCLUSTER_BIN:-${REPO_ROOT}/cluster-toolkit/gcluster}"
  if [[ ! -x "${GCLUSTER_BIN}" ]]; then
    echo "Error: gcluster not found or not executable at '${GCLUSTER_BIN}'." >&2
    echo "Build it (see examples/xmc_lite_job/README.md, section 1C) or set GCLUSTER_BIN." >&2
    exit 1
  fi
  GCLUSTER_VARS="project_id=${PROJECT_ID},deployment_name=${GKE_CLUSTER},region=${GCP_REGION},environment=${XMANAGER_ENV},namespace=${K8S_NAMESPACE}"

  "${GCLUSTER_BIN}" create "${SCRIPT_DIR}/xmc-lite-gke.yaml" \
    --vars "${GCLUSTER_VARS}" \
    -o "${REPO_ROOT}/ctk-out" \
    -l WARNING -w --force

  for group in apis primary compute kubernetes_prereqs; do
    "${GCLUSTER_BIN}" deploy "${REPO_ROOT}/ctk-out/${GKE_CLUSTER}" --only "${group}" --auto-approve
  done
fi

mkdir -p "$(dirname "${KUBECONFIG}")"
gcloud container clusters get-credentials "${GKE_CLUSTER}" \
  --region="${GCP_REGION}" \
  --project="${PROJECT_ID}"

if [[ "${PROVISION_CLUSTER}" == "true" ]]; then
  kubectl create namespace "${K8S_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
  kubectl rollout status deployment/kueue-controller-manager -n kueue-system --timeout=300s
  kubectl apply -n "${K8S_NAMESPACE}" -f "${SCRIPT_DIR}/kueue-queues.yaml"
fi

VENV_DIR="${VENV_DIR:-${SCRIPT_DIR}/.venv}"
if [[ ! -d "${VENV_DIR}" ]]; then
  python3 -m venv "${VENV_DIR}"
fi

"${VENV_DIR}/bin/pip" install --upgrade pip
"${VENV_DIR}/bin/pip" install -r "${SCRIPT_DIR}/requirements.txt"
