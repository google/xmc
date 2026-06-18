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

# --- Helper Functions ---

# Print error and exit
error_exit() {
  echo "Error: ${1}" >&2
  exit 1
}

# Parse standard GitHub tree URLs
# Example: https://github.com/google/xmc/tree/main/api/protos
# Sets REPO_URL, BRANCH, and SUBDIR
parse_github_url() {
  local url="${1}"
  if [[ "${url}" =~ ^(https?://github\.com/[^/]+/[^/]+)/tree/([^/]+)/(.*)$ ]]; then
    REPO_URL="${BASH_REMATCH[1]}.git"
    BRANCH="${BASH_REMATCH[2]}"
    SUBDIR="${BASH_REMATCH[3]}"
  elif [[ "${url}" =~ ^(https?://github\.com/[^/]+/[^/]+)/?$ ]]; then
    REPO_URL="${BASH_REMATCH[1]}.git"
    BRANCH="main"
    SUBDIR="api/protos"
  else
    error_exit "Could not parse GitHub URL: ${url}. Please use format: https://github.com/user/repo/tree/branch/path"
  fi
}

# --- Requirements / Global Constants ---
MIN_GIT_MAJOR=2
MIN_GIT_MINOR=25

# Check Git version for sparse-checkout support (Git 2.25+)
check_git_version() {
  local version
  version=$(git --version | awk '{print $3}')
  # Remove trailing patch versions or characters (e.g. 2.34.1.windows.1)
  IFS='.' read -r major minor _ <<< "${version}"
  
  if [[ ! "${major}" =~ ^[0-9]+$ ]] || [[ ! "${minor}" =~ ^[0-9]+$ ]]; then
    echo "Warning: Could not parse Git version (${version}). Git ${MIN_GIT_MAJOR}.${MIN_GIT_MINOR}+ is recommended." >&2
    return 0
  fi
  
  if (( major < MIN_GIT_MAJOR || (major == MIN_GIT_MAJOR && minor < MIN_GIT_MINOR) )); then
    echo "Warning: Git version is ${version}. Git ${MIN_GIT_MAJOR}.${MIN_GIT_MINOR}+ is required for automatic sparse checkout." >&2
    echo "If downloading remote protos fails, please upgrade Git or download the protos manually and use --protos-loc <local_path>." >&2
  fi
}

# Check for prerequisites
# Note: we use command -v which is standard.
for cmd in git python3; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    error_exit "${cmd} is required but not installed."
  fi
done

check_git_version

# --- Default Parameters ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_XMC_ROOT="$(python3 -c "import os, sys; print(os.path.abspath(sys.argv[1]))" "${SCRIPT_DIR}/../..")"

# Default to local api/protos if run from inside xmc repo, otherwise pull from github
if [ -d "${LOCAL_XMC_ROOT}/api/protos" ]; then
  DEFAULT_PROTOS_LOC="${LOCAL_XMC_ROOT}/api/protos"
else
  DEFAULT_PROTOS_LOC="https://github.com/google/xmc/tree/main/api/protos"
fi

XMC_PROTOS_LOC="${XMC_PROTOS_LOC:-${DEFAULT_PROTOS_LOC}}"
XMANAGER_REPO_URL="https://github.com/google-deepmind/xmanager.git"
XMANAGER_BRANCH="${XMANAGER_BRANCH:-xm-cloud}"
VENV_DIR="${VENV_DIR:-./venv}"

# --- Parse Arguments ---
while [[ $# -gt 0 ]]; do
  case "$1" in
    --protos-loc)
      XMC_PROTOS_LOC="$2"
      shift 2
      ;;
    --xmanager-branch)
      XMANAGER_BRANCH="$2"
      shift 2
      ;;
    --venv)
      VENV_DIR="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --protos-loc <url_or_path>     Location of XMC protos (local directory or GitHub URL)"
      echo "                                 (default: local checkout if available, else github.com/google/xmc)"
      echo "  --xmanager-branch <branch>     XManager git branch (default: ${XMANAGER_BRANCH})"
      echo "  --venv <path>                  Virtual environment target directory (default: ./venv)"
      exit 0
      ;;
    *)
      error_exit "Unknown option: $1"
      ;;
  esac
done

# Resolve VENV_DIR to absolute path
VENV_DIR="$(python3 -c "import os, sys; print(os.path.abspath(sys.argv[1]))" "${VENV_DIR}")"

echo "=== Configuration ==="
echo "Protos Location: ${XMC_PROTOS_LOC}"
echo "XManager Repo:   ${XMANAGER_REPO_URL} (branch: ${XMANAGER_BRANCH})"
echo "Venv Target:     ${VENV_DIR}"
echo "====================="

# Create temporary directory for setup
TEMP_DIR="$(mktemp -d)"
# Clean up temporary clones and builds on completion or failure
trap 'echo "Cleaning up temporary setup files..."; rm -rf "${TEMP_DIR}"' EXIT

XMANAGER_SRC_DIR="${TEMP_DIR}/xmanager"
GOOGLEAPIS_SRC_DIR="${TEMP_DIR}/googleapis"
XMC_PROTOS_SRC_DIR="${TEMP_DIR}/xmc_protos"

# 1. Setup Virtual Environment
if [ ! -d "${VENV_DIR}" ]; then
  echo "Creating virtual environment at ${VENV_DIR}..."
  python3 -m venv "${VENV_DIR}"
fi
source "${VENV_DIR}/bin/activate"

echo "Updating pip and installing compile-time dependencies..."
pip install --upgrade pip
pip install \
  grpcio \
  grpcio-tools \
  google-auth \
  googleapis-common-protos \
  requests

# 2. Clone repositories
echo "Cloning XManager repo (branch: ${XMANAGER_BRANCH})..."
git clone --depth 1 -b "${XMANAGER_BRANCH}" "${XMANAGER_REPO_URL}" "${XMANAGER_SRC_DIR}"

echo "Cloning Google APIs repository..."
git clone --depth 1 https://github.com/googleapis/googleapis.git "${GOOGLEAPIS_SRC_DIR}"

# 3. Pull XMC Protos
if [ -d "${XMC_PROTOS_LOC}" ]; then
  echo "Using local protos from: ${XMC_PROTOS_LOC}"
  mkdir -p "${XMC_PROTOS_SRC_DIR}"
  if [ -d "${XMC_PROTOS_LOC}/api/protos" ]; then
    cp -r "${XMC_PROTOS_LOC}/api/protos/." "${XMC_PROTOS_SRC_DIR}/"
  else
    cp -r "${XMC_PROTOS_LOC}/." "${XMC_PROTOS_SRC_DIR}/"
  fi
else
  echo "Fetching remote protos from: ${XMC_PROTOS_LOC}"
  parse_github_url "${XMC_PROTOS_LOC}"
  
  git clone --depth 1 --no-checkout "${REPO_URL}" "${XMC_PROTOS_SRC_DIR}"
  cd "${XMC_PROTOS_SRC_DIR}"
  git sparse-checkout init --cone
  git sparse-checkout set "${SUBDIR}"
  git checkout "${BRANCH}"
  # Pull subdirectory files to the top-level
  mv "${SUBDIR}"/* .
  cd - > /dev/null
fi

# Validate protos structure
if [ ! -d "${XMC_PROTOS_SRC_DIR}/experiment" ] || [ ! -d "${XMC_PROTOS_SRC_DIR}/xid" ]; then
  error_exit "Protos directory structure is missing required directories ('experiment' or 'xid')."
fi

# 4. Replace proto directories in XManager source
echo "Copying protos into xmanager_cloud..."
rm -rf "${XMANAGER_SRC_DIR}/xmanager_cloud/xid_service/proto"/*
rm -rf "${XMANAGER_SRC_DIR}/xmanager_cloud/experiment_state_server/proto"/*

mkdir -p "${XMANAGER_SRC_DIR}/xmanager_cloud/xid_service/proto"
mkdir -p "${XMANAGER_SRC_DIR}/xmanager_cloud/experiment_state_server/proto"

cp -r "${XMC_PROTOS_SRC_DIR}/xid/"* "${XMANAGER_SRC_DIR}/xmanager_cloud/xid_service/proto/"
cp -r "${XMC_PROTOS_SRC_DIR}/experiment/"* "${XMANAGER_SRC_DIR}/xmanager_cloud/experiment_state_server/proto/"

# 5. Adjust proto imports (rewrite third_party/xmanager_cloud/ to xmanager_cloud/)
echo "Adjusting proto import paths..."
if sed --version >/dev/null 2>&1; then
  SED_I=(sed -i)
else
  SED_I=(sed -i '')
fi

find "${XMANAGER_SRC_DIR}/xmanager_cloud/xid_service/proto" \
     "${XMANAGER_SRC_DIR}/xmanager_cloud/experiment_state_server/proto" \
     -name "*.proto" -type f -exec "${SED_I[@]}" 's|third_party/xmanager_cloud/|xmanager_cloud/|g' {} +

# 6. Compile protos
echo "Compiling xmanager_cloud protos..."
python3 -m grpc_tools.protoc \
  --proto_path="${XMANAGER_SRC_DIR}" \
  --proto_path="${GOOGLEAPIS_SRC_DIR}" \
  --python_out="${XMANAGER_SRC_DIR}" \
  --grpc_python_out="${XMANAGER_SRC_DIR}" \
  "${XMANAGER_SRC_DIR}"/xmanager_cloud/xid_service/proto/*.proto \
  "${XMANAGER_SRC_DIR}"/xmanager_cloud/experiment_state_server/proto/*.proto

# 7. Install package
echo "Installing XManager library..."
# This builds and installs the packages directly to site-packages, so the source directory can be cleaned up.
pip install "${XMANAGER_SRC_DIR}"

# 8. Verify Installation
echo "Verifying imports..."
python3 -c "import xmanager_cloud"
python3 -c "import xmanager_cloud.experiment_state_server.experiment_state_api"
python3 -c "import xmanager_cloud.experiment_state_server.proto.api_pb2"

echo "=================================================="
echo "Installation complete!"
echo "To activate the virtual environment, run:"
echo "  source ${VENV_DIR}/bin/activate"
echo "=================================================="
