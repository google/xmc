#!/bin/bash
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

set -e

PROJECT_ID=$(gcloud config get-value project 2>/dev/null)
if [ -z "${PROJECT_ID}" ]; then
  echo "Error: No active gcloud project found. Please set one using 'gcloud config set project <PROJECT_ID>'." >&2
  exit 1
fi

IMAGE_PATH="${IMAGE_PATH:-us-docker.pkg.dev/${PROJECT_ID}/xmc/managed-xprof-artifact-job:latest}"

echo "Building and pushing image: ${IMAGE_PATH}"
docker build -t "${IMAGE_PATH}" -f Dockerfile .
docker push "${IMAGE_PATH}"
