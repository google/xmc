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

if [[ -z "${PROJECT_ID:-}" ]]; then
  echo "Error: PROJECT_ID environment variable must be set." >&2
  exit 1
fi

IMAGE_NAME="us-docker.pkg.dev/${PROJECT_ID}/xmc/per-job-node-selector:latest"

echo "Building and pushing container image: ${IMAGE_NAME}"
gcloud builds submit --tag "${IMAGE_NAME}" .
echo "Successfully built and pushed ${IMAGE_NAME}"
