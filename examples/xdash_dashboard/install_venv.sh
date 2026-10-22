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
VENV_DIR="${VENV_DIR:-${SCRIPT_DIR}/.venv}"

echo "--> [1/2] Setting up virtual environment, installing dependencies, and compiling local XMC protos..."
python3 "${REPO_ROOT}/deploy/cli/install_xmanager.py" \
  --venv "${VENV_DIR}" \
  --requirements "${SCRIPT_DIR}/requirements.txt"

PIP_BIN="${VENV_DIR}/bin/pip"

echo "--> [2/2] Installing xdash SDK in editable mode..."
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xdash"

echo ""
echo "=== ✅ Virtual environment ready! ==="
echo "To activate and run:"
echo "  source ${VENV_DIR}/bin/activate"
echo "  python3 ${SCRIPT_DIR}/create_dashboard.py"
