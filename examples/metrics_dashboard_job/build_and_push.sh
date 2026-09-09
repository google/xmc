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

PROJECT_ID="${PROJECT_ID:-$(gcloud config get-value project 2>/dev/null || true)}"
if [ -z "${PROJECT_ID}" ]; then
  echo "❌ Error: No active gcloud project found. Set PROJECT_ID or run 'gcloud config set project <PROJECT_ID>'." >&2
  exit 1
fi

IMAGE_PATH="${IMAGE_PATH:-us-docker.pkg.dev/${PROJECT_ID}/xmc/metrics-dashboard-job:latest}"

# Built from the repository root, not this directory: the image needs sdks/python/xmc,
# which lives outside the example and would be unreachable from a narrower context.
echo "--> Building container image: ${IMAGE_PATH}..."
cd "${REPO_ROOT}"
docker build -f "${SCRIPT_DIR}/Dockerfile" -t "${IMAGE_PATH}" .

echo "--> Pushing container image to Artifact Registry..."
docker push "${IMAGE_PATH}"

echo ""
echo "=== ✅ Container successfully built and pushed! ==="
echo "IMAGE_PATH=${IMAGE_PATH}"
echo ""
echo "Pass it to the launch script with:"
echo "  IMAGE_PATH=${IMAGE_PATH} python3 ${SCRIPT_DIR}/launch.py"
