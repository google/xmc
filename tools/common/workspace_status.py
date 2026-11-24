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

"""Workspace status script for Bazel to emit the XMC application version."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

# Support direct execution via `python3 tools/common/workspace_status.py`
# (Bazel's --workspace_status_command runs without PYTHONPATH set).
_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.release_tags import PRE_RELEASE_TAG_GLOB, RELEASE_TAG_GLOBS  # noqa: E402

ENV_VERSION_VAR = "XMC_VERSION"
FALLBACK_VERSION = "0.0.0-unknown"


def _describe_git_version(cwd: Path | None = None) -> str:
    """Runs `git describe --tags --dirty` scoped to SemVer release tags."""
    cmd = ["git", "describe", "--tags", "--dirty"]
    for glob in RELEASE_TAG_GLOBS:
        cmd.extend(["--match", glob])
    cmd.extend(["--exclude", PRE_RELEASE_TAG_GLOB])
    res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
    return res.stdout.strip()


def resolve_version(*, cwd: Path | None = None, warn: bool = False) -> str:
    """Resolves the XMC version: XMC_VERSION env -> git describe -> FALLBACK_VERSION."""
    env_raw = os.environ.get(ENV_VERSION_VAR, "").strip()
    if env_raw:
        if any(ch.isspace() for ch in env_raw):
            if warn:
                print(
                    f"Ignoring invalid {ENV_VERSION_VAR} containing whitespace: {env_raw!r}",
                    file=sys.stderr,
                )
        else:
            return env_raw

    try:
        described = _describe_git_version(cwd=cwd)
    except (subprocess.CalledProcessError, OSError) as e:
        if warn:
            print(f"Error determining version from git describe: {e}", file=sys.stderr)
        return FALLBACK_VERSION

    if not described or any(ch.isspace() for ch in described):
        if warn:
            print(f"Ignoring invalid git describe output: {described!r}", file=sys.stderr)
        return FALLBACK_VERSION
    return described


def main() -> None:
    """Emits STABLE_APP_VERSION for Bazel workspace status stamping."""
    print(f"STABLE_APP_VERSION {resolve_version(warn=True)}")


if __name__ == "__main__":
    main()
