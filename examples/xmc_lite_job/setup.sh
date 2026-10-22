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
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [[ -f "${REPO_ROOT}/.env" ]]; then
  set -o allexport
  # shellcheck disable=SC1091
  source "${REPO_ROOT}/.env"
  set +o allexport
fi

: "${PROJECT_ID:?Set PROJECT_ID in .env or environment}"
: "${GKE_CLUSTER:?Set GKE_CLUSTER in .env or environment}"
: "${GCP_REGION:?Set GCP_REGION in .env or environment}"

export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/config.${XMANAGER_ENV:-xmc-lite}}"
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
