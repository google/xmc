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
VENV_DIR="${SCRIPT_DIR}/.venv"

echo "--> [1/5] Installing XManager library and xmanager_cloud proto bindings..."
cd "${REPO_ROOT}"
python3 "${REPO_ROOT}/deploy/cli/install_xmanager.py" --venv "${VENV_DIR}"

PYTHON_BIN="${VENV_DIR}/bin/python3"
PIP_BIN="${VENV_DIR}/bin/pip"

echo "--> [2/5] Compiling Dashboard Service Python protobuf modules with Bazel..."
bazel build //api/protos/dashboard:dashboard_messages_py_proto //api/protos/dashboard:dashboard_service_py_proto

echo "--> [3/5] Installing clickhouse-connect, grpcio and runtime dependencies..."
"${PIP_BIN}" install --upgrade pip
"${PIP_BIN}" install -r "${SCRIPT_DIR}/requirements.txt"

# sysconfig.get_path('purelib') resolves against the running interpreter, which is the
# venv's; site.getsitepackages() can report the base prefix instead.
SITE_PACKAGES="$("${PYTHON_BIN}" -c "import sysconfig; print(sysconfig.get_path('purelib'))")"
echo "--> [4/5] Copying generated protobuf modules to virtual environment site-packages: ${SITE_PACKAGES}..."
VIRTUAL_IMPORTS="$(bazel info bazel-bin)/api/protos/dashboard/_virtual_imports"
rm -rf "${SITE_PACKAGES}/xmanager_cloud/dashboard_service/proto"
mkdir -p "${SITE_PACKAGES}/xmanager_cloud"
# chmod after *each* copy: Bazel leaves outputs read-only, so subsequent copies
# would otherwise fail to write into existing trees.
for proto_target in dashboard_messages_proto dashboard_service_proto; do
  if [ -d "${VIRTUAL_IMPORTS}/${proto_target}/xmanager_cloud" ]; then
    cp -R "${VIRTUAL_IMPORTS}/${proto_target}/xmanager_cloud/." "${SITE_PACKAGES}/xmanager_cloud/"
    chmod -R u+w "${SITE_PACKAGES}/xmanager_cloud"
  fi
done

echo "--> [5/5] Installing xdash and xmc SDKs in editable mode..."
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xdash"
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xmc"

echo ""
echo "=== ✅ Python environment successfully initialized! ==="
echo "To activate the virtual environment and run the launch script:"
echo "  source ${VENV_DIR}/bin/activate"
echo "  python3 ${SCRIPT_DIR}/launch.py"
