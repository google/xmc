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

"""Git operations and history extraction utilities."""

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


def get_git_file_content(ref: str, file_path: str, repo_dir: Path) -> str:
    """Reads a file at a specific git ref."""
    try:
        res = subprocess.run(
            ["git", "show", f"{ref}:{file_path}"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout
    except subprocess.CalledProcessError:
        return ""


def get_latest_tag_before(target_ref: str, repo_dir: Path) -> str | None:
    """Finds the previous tag before the target reference."""
    try:
        res = subprocess.run(
            ["git", "describe", "--tags", "--abbrev=0", f"{target_ref}^"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout.strip()
    except subprocess.CalledProcessError:
        pass

    try:
        res = subprocess.run(
            ["git", "tag", "--sort=-creatordate"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        tags = [t.strip() for t in res.stdout.splitlines() if t.strip()]
        if target_ref in tags:
            idx = tags.index(target_ref)
            if idx + 1 < len(tags):
                return tags[idx + 1]
        return tags[0] if tags else None
    except subprocess.CalledProcessError:
        return None


def parse_remote_url(repo_dir: Path) -> tuple[str, str]:
    """Extracts GitHub owner and repo from remote origin URL."""
    try:
        res = subprocess.run(
            ["git", "config", "--get", "remote.origin.url"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        url = res.stdout.strip()
        if "github.com" in url:
            if url.endswith(".git"):
                url = url[:-4]
            parts = url.split("github.com")[-1].lstrip(":/").split("/")
            if len(parts) >= 2:
                return parts[0], parts[1]
    except Exception:
        pass
    return "google", "xmc"


def _create_commit_from_header(header_line: str) -> CommitInfo | None:
    """Parses a COMMIT: formatted git log header line into a CommitInfo object."""
    from tools.release.commit_classifier import parse_conventional_commit

    parts = header_line[len("COMMIT:") :].split("\t")
    if len(parts) < 5:
        return None

    full_hash, short_hash, subject, author, commit_date = (
        parts[0],
        parts[1],
        parts[2],
        parts[3],
        parts[4],
    )
    commit_type, scope, desc, pr_number = parse_conventional_commit(subject)
    return CommitInfo(
        commit_hash=full_hash,
        short_hash=short_hash,
        subject=subject,
        author=author,
        date=commit_date,
        commit_type=commit_type,
        scope=scope,
        description=desc,
        pr_number=pr_number,
        files_changed=[],
    )


def get_git_commits(base_ref: str, target_ref: str, repo_dir: Path) -> list[CommitInfo]:
    """Retrieves all git commits and changed files in the given ref range."""
    log_cmd = [
        "git",
        "log",
        "--pretty=format:COMMIT:%H%x09%h%x09%s%x09%an%x09%ad",
        "--date=short",
        "--name-only",
        f"{base_ref}..{target_ref}",
    ]
    try:
        res = subprocess.run(
            log_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Git log failed for range '{base_ref}..{target_ref}': {e.stderr}",
            "Verify that both git references (tags/branches/hashes) exist in the repository.",
        )

    commits: list[CommitInfo] = []
    current_commit: CommitInfo | None = None
    current_files: list[str] = []

    for line in res.stdout.splitlines():
        trimmed = line.strip()
        if not trimmed:
            continue
        if trimmed.startswith("COMMIT:"):
            if current_commit:
                current_commit.files_changed = current_files
                commits.append(current_commit)
                current_files = []
            current_commit = _create_commit_from_header(trimmed)
        elif current_commit:
            current_files.append(trimmed)

    if current_commit:
        current_commit.files_changed = current_files
        commits.append(current_commit)

    return commits
