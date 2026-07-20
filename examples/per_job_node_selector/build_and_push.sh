#!/bin/bash
set -euo pipefail

if [[ -z "${PROJECT_ID:-}" ]]; then
  echo "Error: PROJECT_ID environment variable must be set." >&2
  exit 1
fi

IMAGE_NAME="us-docker.pkg.dev/${PROJECT_ID}/xmc/per-job-node-selector:latest"

echo "Building and pushing container image: ${IMAGE_NAME}"
gcloud builds submit --tag "${IMAGE_NAME}" .
echo "Successfully built and pushed ${IMAGE_NAME}"
