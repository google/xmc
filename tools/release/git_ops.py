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

"""Git subprocess operations and commit log streaming utilities."""

import re
import subprocess
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError
    from tools.release.models import CommitInfo
except ImportError:
    from common.tool_utils import HandledError  # type: ignore[no-redef]
    from models import CommitInfo  # type: ignore[no-redef]


def get_latest_tag_before(target_ref: str, repo_dir: Path | None = None) -> str | None:
    """Finds the most recent semver tag before target_ref."""
    cwd = str(repo_dir) if repo_dir else None
    try:
        cmd = ["git", "describe", "--tags", "--abbrev=0", f"{target_ref}^"]
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
        tag = res.stdout.strip()
        if tag:
            return tag
    except subprocess.CalledProcessError:
        pass

    try:
        cmd = ["git", "tag", "--sort=-v:refname"]
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
        tags = [t.strip() for t in res.stdout.splitlines() if t.strip()]
        for t in tags:
            if t != target_ref:
                return t
    except subprocess.CalledProcessError:
        pass

    return None


def get_git_file_content(ref: str, file_path: str, repo_dir: Path | None = None) -> str | None:
    """Reads a file's content from a specific git revision."""
    cwd = str(repo_dir) if repo_dir else None
    try:
        cmd = ["git", "show", f"{ref}:{file_path}"]
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
        return res.stdout
    except subprocess.CalledProcessError:
        return None


def get_git_commits(
    base_ref: str, target_ref: str, repo_dir: Path | None = None
) -> list[CommitInfo]:
    """Extracts parsed CommitInfo objects between base_ref and target_ref."""
    cwd = str(repo_dir) if repo_dir else None
    delimiter = "---XMC_COMMIT_DELIMITER---"
    field_sep = "---XMC_FIELD_SEP---"
    log_format = f"{delimiter}%n%H{field_sep}%h{field_sep}%s{field_sep}%an{field_sep}%ad"

    cmd = [
        "git",
        "log",
        f"{base_ref}..{target_ref}",
        f"--format={log_format}",
        "--date=short",
        "--name-only",
    ]

    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to fetch git log between '{base_ref}' and '{target_ref}'.",
            f"Error: {e.stderr.strip()}",
        ) from e

    output = res.stdout
    if not output.strip():
        return []

    raw_entries = output.split(delimiter)
    commits: list[CommitInfo] = []

    for entry in raw_entries:
        entry = entry.strip()
        if not entry:
            continue
        lines = entry.splitlines()
        header_line = lines[0]
        files = [line.strip() for line in lines[1:] if line.strip()]

        parts = header_line.split(field_sep)
        if len(parts) < 5:
            continue

        commit_hash, short_hash, subject, author, date = parts[:5]
        pr_number: int | None = None
        pr_match = re.search(r"\(#(\d+)\)\s*$", subject)
        if pr_match:
            pr_number = int(pr_match.group(1))

        commits.append(
            CommitInfo(
                commit_hash=commit_hash,
                short_hash=short_hash,
                subject=subject,
                author=author,
                date=date,
                pr_number=pr_number,
                files_changed=files,
            )
        )

    return commits


def parse_remote_url(remote_url: str) -> tuple[str, str]:
    """Parses owner and repo name from git remote url."""
    clean = remote_url.strip()
    if clean.endswith(".git"):
        clean = clean[:-4]
    match = re.search(r"[:/]([a-zA-Z0-9_.-]+)/([a-zA-Z0-9_.-]+)$", clean)
    if match:
        return match.group(1), match.group(2)
    return "google", "xmc"
