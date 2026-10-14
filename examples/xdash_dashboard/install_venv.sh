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

echo "--> [1/4] Compiling Dashboard Service Python protobuf modules with Bazel..."
cd "${REPO_ROOT}"
bazel build //api/protos/dashboard:dashboard_messages_py_proto //api/protos/dashboard:dashboard_service_py_proto

echo "--> [2/4] Creating Python virtual environment in ${VENV_DIR}..."
if [ ! -d "${VENV_DIR}" ]; then
  python3 -m venv "${VENV_DIR}"
fi

PYTHON_BIN="${VENV_DIR}/bin/python3"
PIP_BIN="${VENV_DIR}/bin/pip"

echo "--> [3/4] Upgrading pip and installing grpcio, protobuf & googleapis-common-protos..."
"${PIP_BIN}" install --upgrade pip
"${PIP_BIN}" install -r "${SCRIPT_DIR}/requirements.txt"

# sysconfig.get_path('purelib') resolves against the running interpreter, which is the
# venv's; site.getsitepackages() can report the base prefix instead.
SITE_PACKAGES="$("${PYTHON_BIN}" -c "import sysconfig; print(sysconfig.get_path('purelib'))")"
echo "--> Copying generated protobuf modules to virtual environment site-packages: ${SITE_PACKAGES}..."
VIRTUAL_IMPORTS="$(bazel info bazel-bin)/api/protos/dashboard/_virtual_imports"
rm -rf "${SITE_PACKAGES}/xmanager_cloud/dashboard_service/proto"
mkdir -p "${SITE_PACKAGES}/xmanager_cloud"
# chmod after *each* copy, not once at the end: Bazel leaves its outputs read-only, so
# the second copy would otherwise fail to write into the tree the first one laid down.
for proto_target in dashboard_messages_proto dashboard_service_proto; do
  cp -R "${VIRTUAL_IMPORTS}/${proto_target}/xmanager_cloud/." "${SITE_PACKAGES}/xmanager_cloud/"
  chmod -R u+w "${SITE_PACKAGES}/xmanager_cloud/dashboard_service"
done

echo "--> [4/4] Installing xdash SDK in editable mode..."
"${PIP_BIN}" install -e "${REPO_ROOT}/sdks/python/xdash"

echo ""
echo "=== ✅ xdash successfully installed into virtual environment! ==="
echo "To activate the virtual environment and run the example:"
echo "  source ${VENV_DIR}/bin/activate"
echo "  python3 ${SCRIPT_DIR}/create_dashboard.py"
