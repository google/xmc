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
import shutil
from pathlib import Path

from tools.common.tool_utils import HandledError, run_tool
from tools.release.git_ops import (
    create_git_tag,
    push_git_tag,
    resolve_repo_owner_and_name,
    resolve_target_commit,
    verify_local_tag,
    verify_remote_tag,
)
from tools.release.github_release_client import (
    publish_via_gh_cli,
    publish_via_rest_api,
)
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


def _validate_preflight(args: argparse.Namespace) -> tuple[str, Path, Path, str]:
    """Validates CLI inputs and files before git mutations."""
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
    notes_content = read_notes_file(notes_path)
    return tag, repo_dir, notes_path, notes_content


def publish_release(args: argparse.Namespace) -> PublishReleaseResult:
    """Creates an annotated git tag and publishes a GitHub Release."""
    tag, repo_dir, notes_path, notes_content = _validate_preflight(args)
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

    tag_created = False
    if not tag_exists_and_matches:
        create_git_tag(tag, target_commit, title, repo_dir, args.force)
        tag_created = True

    tag_pushed = False
    if not args.skip_push:
        push_git_tag(tag, repo_dir, args.force, remote=args.remote)
        tag_pushed = True

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
                force=args.force,
                skip_push=args.skip_push,
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
        if not release_url:
            raise HandledError(
                "Failed to publish release: Release URL is empty.",
                "Check if the release was created successfully on GitHub.",
            )
    except HandledError as e:
        if tag_pushed:
            e.instruction = (
                f"Note: The git tag '{tag}' was pushed to '{args.remote}'. "
                f"You may need to delete it locally and remotely (e.g., `git tag -d {tag} && git push {args.remote} :refs/tags/{tag}`) "
                f"before retrying, or run with `--force`.\n{e.instruction}"
            )
        elif tag_created:
            e.instruction = (
                f"Note: The git tag '{tag}' was created locally. "
                f"You may need to delete it locally (e.g., `git tag -d {tag}`) "
                f"before retrying, or run with `--force`.\n{e.instruction}"
            )
        raise

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
