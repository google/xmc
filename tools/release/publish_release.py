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

"""Helper tool to tag git commits and publish GitHub Releases with release notes."""

import argparse
import os
import shutil
import subprocess
from pathlib import Path

from tools.common.tool_utils import HandledError, run_tool
from tools.release.git_ops import get_git_remote_url, parse_remote_url
from tools.release.models import PublishReleaseResult


def read_notes_file(notes_path: Path) -> str:
    """Reads and validates the release notes markdown file."""
    try:
        with open(notes_path, encoding="utf-8") as f:
            notes_content = f.read().strip()
    except FileNotFoundError:
        raise HandledError(
            f"Release notes file not found: '{notes_path}'",
            "Ensure the release notes markdown file exists before publishing.",
        )
    except (OSError, PermissionError) as e:
        raise HandledError(
            f"Release notes file is inaccessible: '{notes_path}' (Error: {e})",
            "Ensure the release notes markdown file is readable before publishing.",
        )

    if not notes_content:
        raise HandledError(
            "Release notes file is empty.",
            "Write the agreed release notes markdown content into the file before publishing.",
        )
    return notes_content


def _run_git(
    cmd: list[str],
    repo_dir: Path,
    err_prefix: str,
    instruction: str,
    check: bool = True,
) -> subprocess.CompletedProcess:
    """Runs a git command with unified error handling."""
    try:
        return subprocess.run(
            cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            errors="replace",
            check=check,
        )
    except FileNotFoundError as e:
        raise HandledError(
            "git command not found.",
            "Ensure Git is installed and available in your system PATH.",
        ) from e
    except subprocess.CalledProcessError as e:
        detail = e.stderr.strip() if e.stderr else str(e)
        raise HandledError(f"{err_prefix}: {detail}", instruction) from e


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
        check=False,
    )
    if res.returncode != 0:
        raise HandledError(
            f"Failed to check local git tags: {res.stderr.strip() if res.stderr else ''}",
            "Ensure the repository directory is a valid Git repository.",
        )
    if res.stdout.strip() == tag:
        if not force:
            if target_commit:
                tag_commit_res = _run_git(
                    ["git", "rev-parse", f"refs/tags/{tag}^{{commit}}"],
                    repo_dir,
                    "Failed to resolve existing tag commit",
                    "",
                    check=False,
                )
                if (
                    tag_commit_res.returncode == 0
                    and tag_commit_res.stdout.strip() == target_commit
                ):
                    return True
            raise HandledError(
                f"Git tag '{tag}' already exists in local repository.",
                "Verify the version number or use a new incremented version.",
            )
        return False
    return False


def verify_remote_tag(tag: str, repo_dir: Path, force: bool, remote: str = "origin") -> None:
    """Verifies whether the target git tag already exists on the remote repository."""
    if force:
        return
    res = _run_git(
        ["git", "ls-remote", "--tags", remote, f"refs/tags/{tag}"],
        repo_dir,
        "Failed to check remote git tags",
        f"Ensure the remote '{remote}' is configured and accessible.",
        check=False,
    )
    if res.returncode != 0:
        raise HandledError(
            f"Failed to check remote git tags: {res.stderr.strip() if res.stderr else ''}",
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


def _validate_preflight(args: argparse.Namespace) -> tuple[str, Path, Path]:
    """Validates CLI inputs, files, and environment dependencies before git mutations."""
    tag = args.tag.strip()
    if not tag or tag.startswith("-"):
        raise HandledError(
            "Tag name cannot be empty or start with '-'.",
            "Provide a valid tag name, e.g. --tag 0.6.0.",
        )

    repo_dir = Path(args.repo_dir).resolve()
    if not repo_dir.is_dir():
        raise HandledError(
            f"Repository directory '{repo_dir}' does not exist or is not a directory.",
            "Provide a valid path to the git repository root using --repo-dir.",
        )

    notes_path = Path(args.notes_file).resolve()
    read_notes_file(notes_path)

    if not getattr(args, "dry_run", False) and not shutil.which("gh"):
        raise HandledError(
            "GitHub CLI ('gh') is not installed or not in PATH.",
            "Install GitHub CLI (gh) or authenticate with 'gh auth login'.",
        )
    return tag, repo_dir, notes_path


def publish_release(args: argparse.Namespace) -> PublishReleaseResult:
    """Creates an annotated git tag and publishes a GitHub Release."""
    tag, repo_dir, notes_path = _validate_preflight(args)
    title = args.title or f"Release {tag}"
    target_commit = resolve_target_commit(args.target_commit or "HEAD", repo_dir)

    tag_exists_and_matches = verify_local_tag(tag, repo_dir, args.force, target_commit)
    if not args.skip_push:
        verify_remote_tag(tag, repo_dir, args.force, remote=args.remote)

    if getattr(args, "dry_run", False):
        owner, repo = resolve_repo_owner_and_name(repo_dir, args.remote)
        return {
            "status": "dry_run",
            "tag": tag,
            "title": title,
            "release_url": f"https://github.com/{owner}/{repo}/releases/tag/{tag}",
            "release_id": None,
            "is_draft": bool(args.draft),
            "notes_file": str(notes_path),
        }

    if not tag_exists_and_matches:
        create_git_tag(tag, target_commit, title, repo_dir, args.force)
    if not args.skip_push:
        push_git_tag(tag, repo_dir, args.force, remote=args.remote)

    try:
        release_url = publish_via_gh_cli(
            tag,
            title,
            notes_path,
            args.draft,
            args.prerelease,
            target_commit,
            repo_dir,
            remote=args.remote,
            force=args.force,
            skip_push=args.skip_push,
        )
    except HandledError as e:
        if args.skip_push:
            e.instruction = (
                f"Note: The git tag '{tag}' was already created locally. "
                f"You may need to delete it locally (e.g., `git tag -d {tag}`) "
                f"before retrying, or run with `--force`.\n{e.instruction}"
            )
        else:
            e.instruction = (
                f"Note: The git tag '{tag}' was already pushed to '{args.remote}'. "
                f"You may need to delete it locally and remotely (e.g., `git tag -d {tag} && git push {args.remote} :refs/tags/{tag}`) "
                f"before retrying, or run with `--force`.\n{e.instruction}"
            )
        raise

    if not release_url:
        raise HandledError(
            "Failed to publish release: Release URL is empty.",
            "Check if the release was created successfully on GitHub.",
        )

    return {
        "status": "success",
        "tag": tag,
        "title": title,
        "release_url": release_url,
        "release_id": None,
        "is_draft": bool(args.draft),
        "notes_file": str(notes_path),
    }


def create_parser() -> argparse.ArgumentParser:
    """Creates CLI arguments parser."""
    parser = argparse.ArgumentParser(
        description="Tags git repository commit and publishes GitHub Release with release notes."
    )
    parser.add_argument(
        "--tag", "-t", required=True, help="Release tag name (e.g. '0.6.0' or 'v0.6.0')."
    )
    parser.add_argument(
        "--notes-file", "-f", required=True, help="Path to markdown release notes file."
    )
    parser.add_argument("--title", help="Optional release title. Defaults to 'Release <tag>'.")
    parser.add_argument(
        "--target-commit", "-c", default="HEAD", help="Target commit or branch to tag."
    )
    parser.add_argument("--repo-dir", "-r", default=".", help="Path to git repository root.")
    parser.add_argument("--remote", default="origin", help="Git remote name to push tags to.")
    parser.add_argument("--draft", action="store_true", help="Publish release as a draft.")
    parser.add_argument("--prerelease", action="store_true", help="Mark release as pre-release.")
    parser.add_argument(
        "--skip-push",
        action="store_true",
        help="Skip pushing local tag to remote (requires tag to already exist on remote via --verify-tag).",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Validate inputs, commit resolution, and tag availability without creating tags or publishing.",
    )
    parser.add_argument("--force", action="store_true", help="Force overwrite existing tag.")
    return parser


def main() -> None:
    """CLI entrypoint."""
    parser = create_parser()
    run_tool(parser, publish_release)


if __name__ == "__main__":
    main()
