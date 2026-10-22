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

KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/config}"
export KUBECONFIG

mkdir -p "$(dirname "${KUBECONFIG}")"
gcloud container clusters get-credentials "${GKE_CLUSTER}" \
  --region="${GCP_REGION}" \
  --project="${PROJECT_ID}"

VENV_DIR="${VENV_DIR:-${SCRIPT_DIR}/.venv}"
if [[ ! -x "${VENV_DIR}/bin/pip" ]]; then
  python3 -m venv "${VENV_DIR}"
fi

"${VENV_DIR}/bin/pip" install --upgrade pip
"${VENV_DIR}/bin/pip" install -r "${SCRIPT_DIR}/requirements.txt"
if [[ -n "${XMANAGER_VERSION:-}" ]]; then
  "${VENV_DIR}/bin/pip" install "xmanager==${XMANAGER_VERSION}"
fi
