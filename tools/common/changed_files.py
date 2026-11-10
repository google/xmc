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

"""Lists the files a local checkout changes relative to a base ref, uncommitted work included."""

from pathlib import Path

from tools.common.tool_utils import HandledError, run_command

__all__ = ["ChangedFilesError", "list_changed_files", "list_untracked_files"]

_GIT_INSTRUCTION = (
    "Run inside the XMC git checkout with a base ref that exists locally (run `git fetch origin` "
    "if it is missing, or `git fetch --unshallow` in a shallow clone)."
)


class ChangedFilesError(HandledError):
    """Raised when git cannot list the changed files, e.g. for an unknown base ref."""

    def __init__(self, message: str) -> None:
        super().__init__(message, _GIT_INSTRUCTION)


def _run_git(git_args: list[str], repo_root: Path) -> str:
    """Returns the stdout of `git <git_args>` run in repo_root.

    Raises:
        ChangedFilesError: If git is not installed or exits non-zero.
    """
    cmd = ["git", *git_args]
    try:
        proc = run_command(cmd, check=False, capture_output=True, log_cmd=False, cwd=repo_root)
    except OSError as e:
        raise ChangedFilesError(f"Cannot run git: {e}") from e
    if proc.returncode != 0:
        details = proc.stderr.strip() or f"exit code {proc.returncode}"
        raise ChangedFilesError(f"`{' '.join(cmd)}` failed: {details}")
    return proc.stdout


def _split_nul_separated(output: str) -> set[str]:
    """Returns the non-empty paths of NUL-separated git output (`-z`)."""
    return {path for path in output.split("\0") if path}


def list_untracked_files(repo_root: Path) -> list[str]:
    """Returns the sorted repository-relative paths of untracked files that git does not ignore.

    Args:
        repo_root: Top-level directory of the git checkout.

    Returns:
        Sorted list of untracked, non-ignored paths relative to repo_root.

    Raises:
        ChangedFilesError: If git fails.
    """
    untracked = _run_git(["ls-files", "--others", "--exclude-standard", "-z"], repo_root)
    return sorted(_split_nul_separated(untracked))


def list_changed_files(base_ref: str | None, repo_root: Path) -> list[str]:
    """Returns the sorted repository-relative paths changed since base_ref.

    Changed files are tracked files whose working tree content differs from the merge base of
    base_ref and HEAD (committed, staged, and unstaged changes, deletions included), plus
    untracked files that git does not ignore. A rename lists both the old and the new path.

    Args:
        base_ref: Ref whose merge base with HEAD is the comparison point, e.g. "origin/main".
            None compares against HEAD, i.e. lists uncommitted changes only.
        repo_root: Top-level directory of the git checkout.

    Returns:
        Sorted, de-duplicated list of changed paths relative to repo_root.

    Raises:
        ChangedFilesError: If git fails, e.g. because base_ref does not exist locally.
    """
    since = _run_git(["merge-base", base_ref, "HEAD"], repo_root).strip() if base_ref else "HEAD"
    tracked = _run_git(["diff", "--name-only", "--no-renames", "-z", since, "--"], repo_root)
    return sorted(_split_nul_separated(tracked) | set(list_untracked_files(repo_root)))
