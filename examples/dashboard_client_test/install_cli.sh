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

export PIP_EXTRA_INDEX_URL="${PIP_EXTRA_INDEX_URL:-https://pypi.org/simple}"

echo "Installing XManager CLI into ${SCRIPT_DIR}/venv with Dashboard protos from ${REPO_ROOT}/api/protos..."

python3 "${REPO_ROOT}/deploy/cli/install_xmanager.py" \
  --protos-loc "${REPO_ROOT}/api/protos" \
  --venv "${SCRIPT_DIR}/venv"

echo "Installation complete!"
echo "To activate the virtual environment, run:"
echo "  source ${SCRIPT_DIR}/venv/bin/activate"
