#!/usr/bin/env python3
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

"""Chooses which Cluster Toolkit blueprint the deploy and upgrade pipelines compile.

Both pipelines used to name deploy/ctk_v2/xmanager-cloud.yaml inline. --config selects the
deployment overrides file, not the blueprint, so a deployment that needs a different blueprint --
one that reuses an existing cluster and VPC rather than creating them -- could not be driven by
the tooling at all.

Making it a flag alone would trade that for a worse failure: an upgrade re-compiles the blueprint
before applying, so a `gke-upgrade` that forgot the flag would compile the greenfield blueprint
over an existing-infrastructure deployment and plan the creation of a VPC and a cluster. The
choice is therefore recorded beside the compiled deployment when it is made, and upgrades reuse
the recorded value unless told otherwise.
"""

from pathlib import Path

DEFAULT_BLUEPRINT_PATH = "deploy/ctk_v2/xmanager-cloud.yaml"

# Cluster Toolkit's output root, the directory `gcluster create -o` writes deployments into.
DEPLOYMENT_OUTPUT_ROOT = Path("ctk-out")

_RECORD_FILENAME = ".xmc-blueprint"


def record_file(deployment_name: str) -> Path:
    """Returns the path of the file recording which blueprint a deployment was compiled from."""
    return DEPLOYMENT_OUTPUT_ROOT / deployment_name / _RECORD_FILENAME


def record_blueprint(deployment_name: str, blueprint_path: str) -> None:
    """Records the blueprint a deployment was compiled from.

    Silently does nothing when the deployment directory is absent, which happens only if the
    caller records before compiling. Creating the directory here would leave behind a deployment
    directory holding nothing but the record.

    Args:
        deployment_name: Name of the deployment, matching its directory under ctk-out.
        blueprint_path: Repository-relative path of the blueprint that was compiled.
    """
    target = record_file(deployment_name)
    if not target.parent.is_dir():
        return
    target.write_text(f"{blueprint_path}\n", encoding="utf-8")


def recorded_blueprint(deployment_name: str) -> str | None:
    """Returns the blueprint recorded for a deployment, or None when there is no usable record.

    Args:
        deployment_name: Name of the deployment, matching its directory under ctk-out.
    """
    source = record_file(deployment_name)
    if not source.is_file():
        return None
    recorded = source.read_text(encoding="utf-8").strip()
    return recorded or None


def resolve_blueprint(deployment_name: str, requested: str | None = None) -> str:
    """Resolves the blueprint to compile, preferring an explicit request over the record.

    Precedence is explicit request, then the blueprint this deployment was last compiled from,
    then the greenfield default. Deployments made before the record existed have none, so they
    fall through to the default and behave exactly as they did.

    Args:
        deployment_name: Name of the deployment, matching its directory under ctk-out.
        requested: Blueprint path supplied on the command line, if any.

    Returns:
        Repository-relative path of the blueprint to compile.
    """
    if requested and requested.strip():
        return requested.strip()
    return recorded_blueprint(deployment_name) or DEFAULT_BLUEPRINT_PATH
