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
VENV_DIR="${VENV_DIR:-${SCRIPT_DIR}/.venv}"

if [[ ! -d "${VENV_DIR}" ]]; then
  echo "--> [1/3] Creating virtual environment at ${VENV_DIR}..."
  python3 -m venv "${VENV_DIR}"
fi

PIP_BIN="${VENV_DIR}/bin/pip"

echo "--> [2/3] Installing launch-script runtime dependencies..."
"${PIP_BIN}" install --upgrade pip
"${PIP_BIN}" install -r "${SCRIPT_DIR}/requirements.txt"
if [[ -n "${XMANAGER_VERSION:-}" ]]; then
  echo "--> Installing parameterized xmanager version ${XMANAGER_VERSION}..."
  "${PIP_BIN}" install "xmanager==${XMANAGER_VERSION}"
fi

# Editable installs, so a change to either SDK is picked up without reinstalling. xdash
# builds and registers the charts; xmc is not needed to launch, but having it here lets
# you exercise the worker locally against a gateway before building the image.
echo "--> [3/3] Installing the xdash and xmc SDKs in editable mode..."
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xdash"
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xmc"

echo ""
echo "=== ✅ Python environment successfully initialized! ==="
echo "Activate it and launch the example with:"
echo "  source ${VENV_DIR}/bin/activate"
echo "  python3 ${SCRIPT_DIR}/launch.py"
