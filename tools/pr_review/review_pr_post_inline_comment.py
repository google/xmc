#!/usr/bin/env python3
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

"""Helper CLI script to post inline review comments or counter-replies to GitHub PR diffs."""

import argparse
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.pr_review.github_utils import (  # noqa: E402
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    resolve_review_thread,
)


def get_git_username() -> str:
    """Attempt to resolve the current user's authenticated GitHub/git username."""
    try:
        res = call_github_api("/user", user_agent="XMC-PR-Review-Agent")
        if isinstance(res, dict) and res.get("login"):
            return str(res["login"])
    except Exception:
        pass
    return os.environ.get("USER", "unknown")


def _handle_resolve_thread(
    args: argparse.Namespace,
    raw_resolve_thread: str | int,
) -> dict[str, Any]:
    """Resolves a review thread via GitHub GraphQL API."""
    try:
        res = resolve_review_thread(
            args.owner,
            args.repo,
            args.pr_number,
            raw_resolve_thread,
            user_agent="XMC-PR-Review-Agent",
        )
        return {
            "status": "success",
            "owner": args.owner,
            "repo": args.repo,
            "pr_number": args.pr_number,
            "resolved_thread_id": res["thread_id"],
            "is_resolved": res["is_resolved"],
        }
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to resolve review thread '{raw_resolve_thread}': {e}",
            "Verify that the thread ID exists and that you have permission to resolve review threads.",
        ) from e


def _read_comment_message(
    message: str | None,
    message_file: str | None,
) -> str:
    """Reads and validates the comment body from either a string or a file path."""
    if message and message_file:
        raise HandledError(
            "Cannot specify both a message string (--message) and a message file (-f/--message-file).",
            "Provide either direct text via --message or file input via -f/--message-file.",
        )
    if message_file:
        p = Path(message_file)
        if not p.is_file():
            raise HandledError(
                f"Message file '{message_file}' does not exist.",
                "Ensure the message file path is valid and readable.",
            )
        raw_message = p.read_text(encoding="utf-8")
    elif message is not None:
        raw_message = message
    else:
        raise HandledError(
            "Review comment message content is required.",
            "Pass comment content via --message or --message-file.",
        )

    clean_message = raw_message.strip()
    if not clean_message:
        raise HandledError(
            "Review comment body cannot be empty.",
            "Ensure the review comment file or message string contains meaningful text.",
        )
    return clean_message


def post_inline_comment(args: argparse.Namespace) -> dict[str, Any]:
    """Post an inline review comment, counter-reply, or resolve a thread on a PR.

    Handles arbitrary long comments (multi-page markdown descriptions and extensive
    git diff blocks) safely without backtick subshell interpolation or length truncation.
    When using --message-file (-f), multi-line backtick fenced blocks (` ```go ... ``` `)
    and single line backticks are read verbatim and serialized as standard JSON payloads
    sent via stdin to gh API or HTTP urllib, bypassing shell command evaluation completely.
    """
    raw_resolve_thread = getattr(args, "resolve_thread", None)
    if isinstance(raw_resolve_thread, (str, int)) and str(raw_resolve_thread).strip():
        return _handle_resolve_thread(args, raw_resolve_thread)

    raw_message = _read_comment_message(args.message, args.message_file)

    # Append audit usage telemetry
    now_utc = datetime.now(timezone.utc)
    user = get_git_username()
    telemetry = {
        "git_username": user,
        "date": now_utc.strftime("%Y-%m-%d"),
        "time": now_utc.strftime("%H:%M:%S UTC"),
        "pr_number": str(args.pr_number),
        "skill_name": "xmc_review_pr",
    }
    comment_body = f"{raw_message}\n\n<!-- xmc-telemetry: {json.dumps(telemetry)} -->"

    endpoint = f"/repos/{args.owner}/{args.repo}/pulls/{args.pr_number}/comments"

    if args.in_reply_to:
        payload: dict[str, Any] = {
            "body": comment_body,
            "in_reply_to": int(args.in_reply_to),
        }
    else:
        if not (args.commit_id and args.path and args.line is not None):
            raise HandledError(
                "When posting a new inline diff comment (not a thread reply), --commit-id, --path, and --line are required.",
                "Provide --commit-id <sha>, --path <file_path>, and --line <line_number>.",
            )
        payload = {
            "body": comment_body,
            "commit_id": args.commit_id,
            "path": args.path,
            "line": int(args.line),
            "side": args.side or "RIGHT",
        }

    try:
        response = call_github_api(
            endpoint,
            method="POST",
            payload=payload,
            user_agent="XMC-PR-Review-Agent",
        )
    except GithubAuthError as e:
        raise HandledError(
            f"GitHub authentication error: {e}",
            "Ensure you are authenticated with 'gh auth login' or a valid GITHUB_TOKEN.",
        ) from e
    except GithubAPIError as e:
        raise HandledError(
            f"Failed to post inline review comment: {e}",
            "Verify that the commit_id, path, line number, or in_reply_to thread ID exists in the PR diff.",
        ) from e

    return {
        "status": "success",
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "comment_id": response.get("id"),
        "html_url": response.get("html_url"),
        "path": response.get("path", args.path),
        "line": response.get("line", args.line),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Post an inline review comment or counter-reply to a Pull Request diff line."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("pr_number", type=int, help="GitHub Pull Request number.")
    parser.add_argument("--commit-id", help="Target commit SHA for new inline comment.")
    parser.add_argument("--path", help="Relative file path in repository (e.g. pkg/auth/main.go).")
    parser.add_argument("--line", type=int, help="Line number in diff for new inline comment.")
    parser.add_argument(
        "--side",
        choices=["RIGHT", "LEFT"],
        default="RIGHT",
        help="Diff side for line number (RIGHT=head, LEFT=base).",
    )
    parser.add_argument(
        "--in-reply-to",
        type=int,
        help="Existing inline comment ID if counter-replying to a thread.",
    )
    parser.add_argument("--message", help="Direct text string of the review comment.")
    parser.add_argument(
        "-f",
        "--message-file",
        help="File containing markdown content of the review comment (handles arbitrary long comments).",
    )
    parser.add_argument(
        "--resolve-thread",
        help="Review thread ID (GraphQL Node ID 'PRRT_...' or comment database ID) to resolve on GitHub.",
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), post_inline_comment)
