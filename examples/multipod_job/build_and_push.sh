#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

if [ -f "../../.env" ]; then
  source "../../.env"
fi

PROJECT_ID="${PROJECT_ID}"
REGISTRY="${XMC_ACR_TARGET:-us-docker.pkg.dev/${PROJECT_ID}/xmc}"
IMAGE_TAG="${REGISTRY}/multipod-counter:latest"

echo "Building Docker image: ${IMAGE_TAG}..."
docker build --platform linux/amd64 -t "${IMAGE_TAG}" .

echo "Pushing Docker image: ${IMAGE_TAG}..."
docker push "${IMAGE_TAG}"

echo "Successfully built and pushed ${IMAGE_TAG}"
