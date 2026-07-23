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
cd "${SCRIPT_DIR}"

if [ -f "../../.env" ]; then
  # shellcheck disable=SC1091
  source "../../.env"
fi

if [ -z "${PROJECT_ID:-}" ]; then
  echo "Error: PROJECT_ID environment variable must be set." >&2
  exit 1
fi
REGISTRY="${XMC_ACR_TARGET:-us-docker.pkg.dev/${PROJECT_ID}/xmc}"
IMAGE_TAG="${REGISTRY}/multipod-counter:latest"

echo "Building Docker image: ${IMAGE_TAG}..."
docker build --platform linux/amd64 -t "${IMAGE_TAG}" .

echo "Pushing Docker image: ${IMAGE_TAG}..."
docker push "${IMAGE_TAG}"

echo "Successfully built and pushed ${IMAGE_TAG}"
