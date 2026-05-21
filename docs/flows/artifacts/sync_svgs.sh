#!/bin/bash
# Copyright 2025 Google LLC
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
# sync_svgs.sh

if [[ -z "$BUILD_WORKSPACE_DIRECTORY" ]]; then
  echo "Error: This script must be run via 'blaze run'."
  exit 1
fi

PACKAGE_PATH="third_party/xmanager_cloud/(internal doc)"
SOURCE_DIR="$BUILD_WORKSPACE_DIRECTORY/$PACKAGE_PATH"
GENFILES_DIR="$BUILD_WORKSPACE_DIRECTORY/blaze-genfiles/$PACKAGE_PATH"

if [[ ! -d "$GENFILES_DIR" ]]; then
  REAL_GENFILES=$(readlink -f "$BUILD_WORKSPACE_DIRECTORY/blaze-genfiles")
  GENFILES_DIR="$REAL_GENFILES/$PACKAGE_PATH"
fi

if [[ ! -d "$GENFILES_DIR" ]]; then
  echo "Error: Generated SVGs not found in $GENFILES_DIR."
  echo "Please run: blaze build //$PACKAGE_PATH:all"
  exit 1
fi

echo "Copying SVGs to $SOURCE_DIR..."
cp -v "$GENFILES_DIR"/*.svg "$SOURCE_DIR/"
