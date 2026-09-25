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

import os
import re
import subprocess
import sys
from pathlib import Path

from tools.common.tool_utils import HandledError
from tools.release.models import CommitInfo


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

    return None


def get_git_file_content(ref: str, file_path: str, repo_dir: Path | None = None) -> str | None:
    """Reads a file's content from a specific git revision."""
    cwd = str(repo_dir) if repo_dir else None
    try:
        posix_path = Path(file_path).as_posix()
        cmd = ["git", "show", f"{ref}:{posix_path}"]
        res = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            errors="replace",
            check=True,
            cwd=cwd,
        )
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
    log_format = f"{delimiter}%n%H{field_sep}%h{field_sep}%an{field_sep}%ad{field_sep}%s"

    cmd = [
        "git",
        "log",
        f"{base_ref}..{target_ref}",
        f"--format={log_format}",
        "--date=short",
        "--name-only",
    ]

    try:
        res = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            errors="replace",
            check=True,
            cwd=cwd,
        )
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

        parts = header_line.split(field_sep, 4)
        if len(parts) < 5:
            sys.stderr.write(f"Warning: Skipping malformed commit entry: {header_line}\n")
            continue

        commit_hash, short_hash, author, date, subject = parts
        pr_number: int | None = None
        pr_match = re.search(r"\(#(\d+)\)\s*$", subject)
        if pr_match:
            pr_number = int(pr_match.group(1))

        commit_type = "other"
        scope = ""
        description = subject
        cc_match = re.match(r"^([a-zA-Z0-9_-]+)(?:\(([a-zA-Z0-9_-]+)\))?(!)?:\s*(.*)$", subject)
        if cc_match:
            commit_type = cc_match.group(1)
            scope = cc_match.group(2) or ""
            description = cc_match.group(4)
            if pr_match:
                description = re.sub(r"\s*\(#\d+\)\s*$", "", description)

        commits.append(
            CommitInfo(
                commit_hash=commit_hash,
                short_hash=short_hash,
                subject=subject,
                author=author,
                date=date,
                commit_type=commit_type,
                scope=scope,
                description=description,
                pr_number=pr_number,
                files_changed=files,
            )
        )

    return commits


def get_git_remote_url(repo_dir: Path | None = None, remote: str = "origin") -> str:
    """Retrieves the remote URL for the local repository."""
    cwd = str(repo_dir) if repo_dir else None
    try:
        cmd = ["git", "remote", "get-url", remote]
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=cwd)
        return res.stdout.strip()
    except subprocess.CalledProcessError as e:
        err_msg = e.stderr.strip() if e.stderr else str(e)
        raise HandledError(
            f"Failed to retrieve git remote URL for '{remote}': {err_msg}",
            "Ensure you are in a valid git repository with the remote configured, or set GITHUB_OWNER and GITHUB_REPO env vars.",
        ) from e
    except FileNotFoundError as e:
        raise HandledError(
            f"git command not found: {e}",
            "Ensure Git is installed and available in your system PATH.",
        ) from e


def parse_remote_url(remote_url: str) -> tuple[str, str]:
    """Parses owner and repo name from git remote url."""
    clean = str(remote_url).strip().rstrip("/")
    if clean.endswith(".git"):
        clean = clean[:-4]
    match = re.search(r"[:/]([a-zA-Z0-9_.-]+)/([a-zA-Z0-9_.-]+)$", clean)
    if match:
        return match.group(1), match.group(2)
    return "google", "xmc"


def _run_git(
    cmd: list[str],
    repo_dir: Path,
    err_prefix: str,
    instruction: str,
    check: bool = True,
) -> subprocess.CompletedProcess:
    """Runs a git command with unified error handling."""
    try:
        res = subprocess.run(
            cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            errors="replace",
            check=False,
        )
    except FileNotFoundError as e:
        raise HandledError(
            "git command not found.",
            "Ensure Git is installed and available in your system PATH.",
        ) from e
    except subprocess.CalledProcessError as e:
        detail = e.stderr.strip() if e.stderr else str(e)
        raise HandledError(f"{err_prefix}: {detail}", instruction) from e

    if check and res.returncode != 0:
        detail = (
            res.stderr.strip() if getattr(res, "stderr", None) else f"exit code {res.returncode}"
        )
        raise HandledError(f"{err_prefix}: {detail}", instruction)
    return res


def _get_local_tag_commit(tag: str, repo_dir: Path) -> str | None:
    """Resolves the commit SHA pointed to by an existing local tag, or None if unresolvable."""
    res = _run_git(
        ["git", "rev-parse", f"refs/tags/{tag}^{{commit}}"],
        repo_dir,
        "Failed to resolve existing tag commit",
        "",
        check=False,
    )
    return res.stdout.strip() if res.returncode == 0 else None


def verify_local_tag(
    tag: str, repo_dir: Path, force: bool, target_commit: str | None = None
) -> bool:
    """Verifies whether the target git tag already exists locally.

    Returns True if the tag exists and matches target_commit (can be reused), False otherwise.
    """
    res = _run_git(
        ["git", "tag", "-l", tag],
        repo_dir,
        "Failed to check local git tags",
        "Ensure the repository directory is a valid Git repository.",
    )
    if res.stdout.strip() != tag or force:
        return False

    if target_commit and _get_local_tag_commit(tag, repo_dir) == target_commit:
        return True

    raise HandledError(
        f"Git tag '{tag}' already exists in local repository.",
        "Verify the version number, specify --force to overwrite, or ensure the target commit matches.",
    )


def verify_remote_tag(tag: str, repo_dir: Path, force: bool, remote: str = "origin") -> None:
    """Verifies whether the target git tag already exists on the remote repository."""
    if force:
        return
    res = _run_git(
        ["git", "ls-remote", "--tags", remote, f"refs/tags/{tag}"],
        repo_dir,
        "Failed to check remote git tags",
        f"Ensure the remote '{remote}' is configured and accessible.",
    )
    if res.stdout.strip():
        raise HandledError(
            f"Git tag '{tag}' already exists on remote repository '{remote}'.",
            "Verify the version number or use --force to overwrite.",
        )


def create_git_tag(tag: str, target_commit: str, title: str, repo_dir: Path, force: bool) -> None:
    """Creates an annotated git tag locally."""
    cmd = ["git", "tag", "-a", tag, target_commit, "-m", title]
    if force:
        cmd.append("--force")
    _run_git(
        cmd,
        repo_dir,
        f"Failed to create git tag '{tag}'",
        "Check git repository status and target commit reference.",
    )


def push_git_tag(tag: str, repo_dir: Path, force: bool, remote: str = "origin") -> None:
    """Pushes a local git tag to the remote repository."""
    cmd = ["git", "push", remote, tag]
    if force:
        cmd.append("--force")
    try:
        _run_git(cmd, repo_dir, f"Failed to push git tag '{tag}' to {remote}", "")
    except HandledError as e:
        if not e.instruction:
            stderr = str(e)
            if "already exists" in stderr or "rejected" in stderr:
                e.instruction = f"The tag may already exist on the remote repository '{remote}'. Verify the version or use --force."
            else:
                e.instruction = f"Ensure you have write permissions to push git tags to remote repository '{remote}'."
        raise


def resolve_repo_owner_and_name(repo_dir: Path, remote: str = "origin") -> tuple[str, str]:
    """Resolves owner and repository name from env vars with git remote fallback."""
    owner = os.environ.get("GITHUB_OWNER")
    repo = os.environ.get("GITHUB_REPO")
    if not owner or not repo:
        gh_repo_env = os.environ.get("GITHUB_REPOSITORY")
        if gh_repo_env and "/" in gh_repo_env:
            parts = gh_repo_env.split("/", 1)
            owner = owner or parts[0]
            repo = repo or parts[1]
    if not owner or not repo:
        detected_owner, detected_repo = parse_remote_url(get_git_remote_url(repo_dir, remote))
        owner = owner or detected_owner
        repo = repo or detected_repo
    return owner, repo


def resolve_target_commit(target_commit: str, repo_dir: Path) -> str:
    """Resolves target commit/ref to a full commit SHA locally."""
    try:
        res = _run_git(
            ["git", "rev-parse", f"{target_commit}^{{commit}}"],
            repo_dir,
            f"Failed to resolve target commit '{target_commit}'",
            "Ensure the target commit, branch, or tag exists in the repository.",
        )
        return res.stdout.strip()
    except HandledError as e:
        if "git command not found" in str(e):
            raise HandledError(
                f"Failed to resolve target commit '{target_commit}': {e}",
                "Ensure Git is installed and available in your system PATH.",
            ) from e
        raise
