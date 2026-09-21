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

"""GitHub release publishing client transports."""

import subprocess
from pathlib import Path

from tools.common.tool_utils import HandledError
from tools.release.git_ops import resolve_repo_owner_and_name


def _build_gh_release_cmd(
    tag: str,
    title: str,
    notes_path: Path,
    owner: str,
    repo: str,
    draft: bool,
    prerelease: bool,
    target_commit: str,
    force: bool,
    skip_push: bool = False,
) -> list[str]:
    """Builds the gh release create command arguments."""
    gh_cmd = [
        "gh",
        "release",
        "create",
        tag,
        "--title",
        title,
        "--notes-file",
        str(notes_path),
        "--repo",
        f"{owner}/{repo}",
    ]
    if draft:
        gh_cmd.append("--draft")
    if prerelease:
        gh_cmd.append("--prerelease")
    if target_commit and not skip_push:
        gh_cmd.extend(["--target", target_commit])
    if force:
        gh_cmd.append("--clobber")
    if skip_push:
        gh_cmd.append("--verify-tag")
    return gh_cmd


def publish_via_gh_cli(
    tag: str,
    title: str,
    notes_path: Path,
    draft: bool,
    prerelease: bool,
    target_commit: str,
    repo_dir: Path,
    remote: str = "origin",
    force: bool = False,
    skip_push: bool = False,
) -> str:
    """Publishes a GitHub release using the gh CLI."""
    owner, repo = resolve_repo_owner_and_name(repo_dir, remote)
    gh_cmd = _build_gh_release_cmd(
        tag,
        title,
        notes_path,
        owner,
        repo,
        draft,
        prerelease,
        target_commit,
        force,
        skip_push=skip_push,
    )
    try:
        res = subprocess.run(
            gh_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            errors="replace",
            check=True,
        )
        return res.stdout.strip()
    except FileNotFoundError as e:
        raise HandledError(
            "GitHub CLI ('gh') is not installed or not in PATH.",
            "Install GitHub CLI (gh) or authenticate with 'gh auth login'.",
        ) from e
    except subprocess.CalledProcessError as e:
        err_msg = e.stderr.strip() if e.stderr else str(e)
        raise HandledError(
            f"Failed to create GitHub release via gh CLI: {err_msg}",
            "Ensure gh CLI is authenticated and has release creation permissions.",
        ) from e
