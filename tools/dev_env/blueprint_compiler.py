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

"""Compiles the XMC Cluster Toolkit blueprint from a clean staging area.

`gcluster create --force` copies files referenced via `ghpc_stage()` into
`<output_dir>/<deployment_name>/.ghpc/staged/` but never deletes files that
were removed from the repository. Stale Helm templates would therefore keep
being rendered. Purging the staging area before each compile guarantees that
the staged copy mirrors the current checkout; `gcluster create` re-stages
every referenced file.
"""

import hashlib
import shutil
from pathlib import Path

from tools.common.tool_utils import log_info, run_command
from tools.dev_env.chart_fingerprint import compute_chart_content_hash

BLUEPRINT_PATH = "deploy/ctk_v2/xmanager-cloud.yaml"
HELM_CHART_DIR = Path("deploy/helm")
POSTGRES_MIGRATION_DIRS: tuple[Path, ...] = (
    Path("internal/storage/postgres/migrations/ess"),
    Path("internal/storage/postgres/migrations/xid"),
)
DEFAULT_OUTPUT_DIR = Path("ctk-out")


def _resolve_staged_dir(output_dir: Path, deployment_name: str) -> Path:
    """Returns the staging directory, rejecting unsafe deployment layouts.

    Args:
        output_dir: Cluster Toolkit output directory (may itself be a symlink).
        deployment_name: Deployment name; must be a single plain path component.

    Returns:
        The absolute `<output_dir>/<deployment_name>/.ghpc/staged` path.

    Raises:
        ValueError: If the name or any path component below `output_dir` could
            redirect deletion outside the staging directory.
    """
    if deployment_name in ("", ".", "..") or Path(deployment_name).name != deployment_name:
        raise ValueError(f"Unsafe deployment name: {deployment_name!r}")
    root = output_dir.resolve()
    staged_dir = root / deployment_name / ".ghpc" / "staged"
    for component in (staged_dir.parent.parent, staged_dir.parent, staged_dir):
        if component.is_symlink():
            raise ValueError(f"Refusing to purge through symlink: {component}")
    return staged_dir


def purge_staged_artifacts(output_dir: Path, deployment_name: str) -> None:
    """Deletes the Cluster Toolkit staging directory of a deployment.

    Args:
        output_dir: Cluster Toolkit output directory (e.g. `ctk-out`).
        deployment_name: Deployment name whose staging directory is purged.

    Raises:
        ValueError: If the staging directory path is unsafe to delete.
    """
    staged_dir = _resolve_staged_dir(output_dir, deployment_name)
    if not staged_dir.is_dir():
        return
    log_info(f"Purging stale staged blueprint artifacts under '{staged_dir}'...")
    shutil.rmtree(staged_dir)


def _compute_release_content_hash() -> str:
    """Hashes the Helm chart and embedded PostgreSQL migration directories."""
    parts = [compute_chart_content_hash(d) for d in (HELM_CHART_DIR, *POSTGRES_MIGRATION_DIRS)]
    return hashlib.sha256("\n".join(parts).encode()).hexdigest()


def compile_blueprint(
    config_path: str,
    deployment_name: str,
    output_dir: Path = DEFAULT_OUTPUT_DIR,
    *,
    enable_components: bool = False,
) -> None:
    """Purges staged artifacts and re-compiles the XMC blueprint.

    The umbrella chart and migration SQL content hash is passed as the
    `helm_chart_content_hash` blueprint variable so that template-only or
    SQL-only changes alter the Helm release inputs and trigger a real upgrade.

    Args:
        config_path: Path to the deployment configuration YAML.
        deployment_name: Deployment name used to locate the staging directory.
        output_dir: Cluster Toolkit output directory.
        enable_components: When True, passes `--vars enable_components=true` to gcluster.
    """
    purge_staged_artifacts(output_dir, deployment_name)
    chart_hash = _compute_release_content_hash()
    extra_vars = ["--vars", f"enable_components={'true' if enable_components else 'false'}"]
    run_command(
        [
            "./cluster-toolkit/gcluster",
            "create",
            BLUEPRINT_PATH,
            "-d",
            config_path,
            "-o",
            str(output_dir),
            "--vars",
            f"helm_chart_content_hash={chart_hash}",
            *extra_vars,
            "-l",
            "WARNING",
            "-w",
            "--force",
        ]
    )
