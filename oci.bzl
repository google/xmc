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

def _oci_push_script(name, service_name, tarball_target, tarball_file):
    native.genrule(
        name = name + "_push",
        srcs = [":" + tarball_target],
        outs = [name + "_push.sh"],
        cmd = """
cat << 'EOF' > $@
#!/usr/bin/env bash
set -euo pipefail

if [[ -z "$${{XMC_ACR_TARGET:-}}" ]]; then
  echo "ERROR: XMC_ACR_TARGET environment variable is not set." >&2
  exit 1
fi

XMC_ENV="$${{XMC_ENV:-dev}}"
REPO="$${{XMC_ACR_TARGET}}/{service_name}-$${{XMC_ENV}}"
TAG="$${{BUILD_TAG:-latest}}"

if command -v docker &>/dev/null; then
  CONTAINER_CMD="docker"
elif command -v podman &>/dev/null; then
  CONTAINER_CMD="podman"
else
  echo "ERROR: Neither docker nor podman command was found." >&2
  exit 1
fi

# Find the runfile path of the tarball
RUNFILES_DIR="$${{BASH_SOURCE[0]}}.runfiles"
if [[ ! -d "$$RUNFILES_DIR" ]]; then
  RUNFILES_DIR="$$(dirname "$${{BASH_SOURCE[0]}}")/../.."
fi

TARBALL_PATH=$$(find "$$RUNFILES_DIR" -name "{tarball_file}" -print -quit)
if [[ -z "$$TARBALL_PATH" || ! -f "$$TARBALL_PATH" ]]; then
  # Fallback to local directory
  DIR="$$(dirname "$${{BASH_SOURCE[0]}}")"
  TARBALL_PATH="$$DIR/{tarball_file}"
fi

if [[ ! -f "$$TARBALL_PATH" ]]; then
  TARBALL_PATH="$$(find . -name '{tarball_file}' -print -quit)"
fi

if [[ -z "$$TARBALL_PATH" || ! -f "$$TARBALL_PATH" ]]; then
  echo "ERROR: Could not find tarball {tarball_file}." >&2
  exit 1
fi

echo "Loading image from tarball $${{TARBALL_PATH}}..."
LOAD_OUT=$$($${{CONTAINER_CMD}} load -i "$${{TARBALL_PATH}}")
echo "$$LOAD_OUT"

# Parse loaded image tag
IMAGE_ID=$$(echo "$$LOAD_OUT" | sed -n 's/^Loaded image: //p')
if [[ -z "$$IMAGE_ID" ]]; then
  IMAGE_ID="{service_name}:latest"
fi

echo "Tagging image: $$IMAGE_ID -> $${{REPO}}:$${{TAG}}"
$${{CONTAINER_CMD}} tag "$$IMAGE_ID" "$${{REPO}}:$${{TAG}}"

echo "Pushing image to $${{REPO}}:$${{TAG}}..."
$${{CONTAINER_CMD}} push "$${{REPO}}:$${{TAG}}"
EOF
chmod +x $@
""".format(
            service_name = service_name,
            tarball_file = tarball_file,
        ),
        executable = True,
        visibility = ["//visibility:public"],
    )

def oci_image_for_service(name, binary):
    # Split binary name
    binary_name = binary.split(":")[-1]
    tarball_target = name + "_tarball"
    tarball_file = name + "_tarball.tar"

    service_name = name
    if service_name.endswith("_image"):
        service_name = service_name[:-6]
    service_name = service_name.replace("_", "-")

    # 1. Build the loadable OCI tarball!
    native.genrule(
        name = tarball_target,
        srcs = [binary],
        outs = [tarball_file],
        cmd = "python3 $(location //tools:build_oci.py) $< " + binary_name + " " + name + ":latest $@",
        tools = ["//tools:build_oci.py"],
        visibility = ["//visibility:public"],
    )

    # 2. Build the push script genrule target directly!
    _oci_push_script(name, service_name, tarball_target, tarball_file)

def oci_image_for_ui(name, srcs = ["//cmd/ui:ui_srcs"], builder = "//cmd/ui:build_ui_oci.py"):
    tarball_target = name + "_tarball"
    tarball_file = name + "_tarball.tar"

    service_name = name
    if service_name.endswith("_image"):
        service_name = service_name[:-6]
    service_name = service_name.replace("_", "-")

    native.genrule(
        name = tarball_target,
        srcs = srcs + [builder],
        outs = [tarball_file],
        cmd = "python3 $(location " + builder + ") $@ " + service_name + "-image:latest",
        tools = [builder],
        visibility = ["//visibility:public"],
    )

    _oci_push_script(name, service_name, tarball_target, tarball_file)
