#!/bin/bash
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

PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project 2>/dev/null)}"
if [[ -z "${PROJECT_ID}" ]]; then
  echo "Error: PROJECT_ID is not set and gcloud project is empty." >&2
  exit 1
fi

IMAGE_PATH="us-docker.pkg.dev/${PROJECT_ID}/xmc/metrics-job:latest"

echo "Building Docker image: ${IMAGE_PATH}..."
docker build -t "${IMAGE_PATH}" -f "${SCRIPT_DIR}/Dockerfile" "${REPO_ROOT}"

echo "Pushing Docker image: ${IMAGE_PATH}..."
docker push "${IMAGE_PATH}"

echo "Successfully built and pushed ${IMAGE_PATH}"
