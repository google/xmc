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
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError, run_tool
    from tools.pr_review.github_utils import (
        GithubAPIError,
        GithubAuthError,
        call_github_api,
    )
    from tools.release.git_ops import get_git_remote_url, parse_remote_url
    from tools.release.models import PublishReleaseResult
except ImportError:
    from git_ops import get_git_remote_url, parse_remote_url  # type: ignore[no-redef]
    from github_utils import (  # type: ignore[no-redef]
        GithubAPIError,
        GithubAuthError,
        call_github_api,
    )
    from models import PublishReleaseResult  # type: ignore[no-redef]
    from tool_utils import HandledError, run_tool  # type: ignore[no-redef]


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


def verify_local_tag(tag: str, repo_dir: Path, force: bool) -> None:
    """Verifies whether the target git tag already exists locally."""
    try:
        res = subprocess.run(
            ["git", "tag", "-l", tag],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        raise HandledError(
            "git command not found.",
            "Ensure Git is installed and available in your system PATH.",
        )
    if res.returncode != 0:
        raise HandledError(
            f"Failed to check local git tags: {res.stderr.strip() if res.stderr else ''}",
            "Ensure the repository directory is a valid Git repository.",
        )
    if res.stdout.strip() == tag and not force:
        raise HandledError(
            f"Git tag '{tag}' already exists in local repository.",
            "Verify the version number or use a new incremented version.",
        )


def create_git_tag(
    tag: str,
    target_commit: str,
    title: str,
    repo_dir: Path,
    force: bool,
) -> None:
    """Creates an annotated git tag locally."""
    tag_cmd = ["git", "tag", "-a", tag, target_commit, "-m", title]
    if force:
        tag_cmd.append("--force")
    try:
        subprocess.run(
            tag_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as e:
        err_msg = e.stderr.strip() if e.stderr else str(e)
        raise HandledError(
            f"Failed to create git tag '{tag}': {err_msg}",
            "Check git repository status and target commit reference.",
        )
    except FileNotFoundError:
        raise HandledError(
            "git command not found.",
            "Ensure Git is installed and available in your system PATH.",
        )


def push_git_tag(tag: str, repo_dir: Path, force: bool, remote: str = "origin") -> None:
    """Pushes a local git tag to the remote repository."""
    push_cmd = ["git", "push", remote, tag]
    if force:
        push_cmd.append("--force")
    try:
        subprocess.run(
            push_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as e:
        stderr = e.stderr or ""
        if "already exists" in stderr or "rejected" in stderr:
            msg = f"The tag may already exist on the remote repository '{remote}'. Verify the version or use --force."
        else:
            msg = f"Ensure you have write permissions to push git tags to remote repository '{remote}'."
        raise HandledError(
            f"Failed to push git tag '{tag}' to {remote}: {stderr.strip()}",
            msg,
        )
    except FileNotFoundError:
        raise HandledError(
            "git command not found.",
            "Ensure Git is installed and available in your system PATH.",
        )


def resolve_repo_owner_and_name(repo_dir: Path, remote: str = "origin") -> tuple[str, str]:
    """Resolves owner and repository name from env vars with git remote fallback."""
    owner = os.environ.get("GITHUB_OWNER")
    repo = os.environ.get("GITHUB_REPO")
    if not owner or not repo:
        detected_owner, detected_repo = parse_remote_url(get_git_remote_url(repo_dir, remote))
        owner = owner or detected_owner
        repo = repo or detected_repo
    return owner, repo


def resolve_target_commit(target_commit: str, repo_dir: Path) -> str:
    """Resolves target commit/ref to a full commit SHA locally."""
    try:
        res = subprocess.run(
            ["git", "rev-parse", target_commit],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        err_msg = (
            e.stderr.strip()
            if isinstance(e, subprocess.CalledProcessError) and e.stderr
            else str(e)
        )
        raise HandledError(
            f"Failed to resolve target commit '{target_commit}': {err_msg}",
            "Ensure the target commit, branch, or tag exists in the repository.",
        )


def publish_via_gh_cli(
    tag: str,
    title: str,
    notes_path: Path,
    draft: bool,
    prerelease: bool,
    target_commit: str,
    repo_dir: Path,
    remote: str = "origin",
) -> str:
    """Publishes a GitHub release using the gh CLI."""
    owner, repo = resolve_repo_owner_and_name(repo_dir, remote)

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
    if target_commit:
        gh_cmd.extend(["--target", target_commit])

    try:
        res = subprocess.run(
            gh_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout.strip()
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to create GitHub release via gh CLI: {e.stderr}",
            "Ensure gh CLI is authenticated and has release creation permissions.",
        )


def publish_via_rest_api(
    tag: str,
    title: str,
    notes_content: str,
    draft: bool,
    prerelease: bool,
    target_commit: str,
    repo_dir: Path,
    remote: str = "origin",
) -> tuple[str, int | None]:
    """Publishes a GitHub release using the GitHub REST API."""
    owner, repo = resolve_repo_owner_and_name(repo_dir, remote)

    endpoint = f"/repos/{owner}/{repo}/releases"
    payload = {
        "tag_name": tag,
        "target_commitish": target_commit,
        "name": title,
        "body": notes_content,
        "draft": draft,
        "prerelease": prerelease,
    }
    try:
        resp = call_github_api(
            endpoint,
            method="POST",
            payload=payload,
            user_agent="XMC-Release-Publisher",
        )
        if resp:
            return resp.get("html_url", ""), resp.get("id")
        return "", None
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to create GitHub release via REST API: {e}",
            "Check GitHub token permissions.",
        )


def publish_release(args: argparse.Namespace) -> PublishReleaseResult:
    """Creates an annotated git tag and publishes a GitHub Release."""
    tag = args.tag.strip()
    if not tag:
        raise HandledError(
            "Tag name cannot be empty.",
            "Provide a valid tag name, e.g. --tag 0.6.0.",
        )

    repo_dir = Path(args.repo_dir).resolve()
    if not repo_dir.is_dir():
        raise HandledError(
            f"Repository directory '{repo_dir}' does not exist or is not a directory.",
            "Provide a valid path to the git repository root using --repo-dir.",
        )

    notes_path = Path(args.notes_file).resolve()
    notes_content = read_notes_file(notes_path)
    title = args.title or f"Release {tag}"
    target_commit = resolve_target_commit(args.target_commit or "HEAD", repo_dir)

    # 1. Verify and create git tag
    verify_local_tag(tag, repo_dir, args.force)
    create_git_tag(tag, target_commit, title, repo_dir, args.force)

    # 2. Push git tag to remote (if not skipped)
    if not args.skip_push:
        push_git_tag(tag, repo_dir, args.force, remote=args.remote)

    # 3. Publish release via gh CLI or REST API fallback
    try:
        if shutil.which("gh"):
            release_url = publish_via_gh_cli(
                tag,
                title,
                notes_path,
                args.draft,
                args.prerelease,
                target_commit,
                repo_dir,
                remote=args.remote,
            )
            release_id = None
        else:
            release_url, release_id = publish_via_rest_api(
                tag,
                title,
                notes_content,
                args.draft,
                args.prerelease,
                target_commit,
                repo_dir,
                remote=args.remote,
            )
    except HandledError as e:
        if not args.skip_push:
            e.instruction = (
                f"Note: The git tag '{tag}' was already pushed to '{args.remote}'. "
                f"You may need to delete it locally and remotely (e.g., `git tag -d {tag} && git push {args.remote} :refs/tags/{tag}`) "
                f"before retrying, or run with `--force`.\n{e.instruction}"
            )
        raise e

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
        "release_id": release_id,
        "is_draft": bool(args.draft),
        "notes_file": str(notes_path),
    }


def create_parser() -> argparse.ArgumentParser:
    """Creates CLI arguments parser."""
    parser = argparse.ArgumentParser(
        description="Tags git repository commit and publishes GitHub Release with release notes."
    )
    parser.add_argument(
        "--tag",
        "-t",
        required=True,
        help="Release tag name (e.g. '0.6.0' or 'v0.6.0').",
    )
    parser.add_argument(
        "--notes-file",
        "-f",
        required=True,
        help="Path to markdown release notes file.",
    )
    parser.add_argument(
        "--title",
        help="Optional release title. Defaults to 'Release <tag>'.",
    )
    parser.add_argument(
        "--target-commit",
        "-c",
        default="HEAD",
        help="Target commit or branch to tag. Defaults to 'HEAD'.",
    )
    parser.add_argument(
        "--repo-dir",
        "-r",
        default=".",
        help="Path to git repository root. Defaults to current directory.",
    )
    parser.add_argument(
        "--remote",
        default="origin",
        help="Git remote name to push tags to and retrieve repository URL from. Defaults to 'origin'.",
    )
    parser.add_argument(
        "--draft",
        action="store_true",
        help="Publish release as a draft.",
    )
    parser.add_argument(
        "--prerelease",
        action="store_true",
        help="Mark release as pre-release.",
    )
    parser.add_argument(
        "--skip-push",
        action="store_true",
        help="Skip pushing tag to remote repository.",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Force overwrite existing tag.",
    )
    return parser


def main() -> None:
    """CLI entrypoint."""
    parser = create_parser()
    run_tool(parser, publish_release)


if __name__ == "__main__":
    main()
