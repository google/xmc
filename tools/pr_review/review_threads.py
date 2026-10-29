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

"""Lists Pull Request review threads and resolves the ones you have replied to.

Subcommands:
  list <owner> <repo> <pr_number> [--unresolved-only]
  resolve-replied <owner> <repo> <pr_number> [--dry-run]
"""

import argparse
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.pr_review.github_utils import (  # noqa: E402
    GithubAPIError,
    GithubAuthError,
    get_authenticated_login,
    list_review_threads,
    resolve_review_thread,
)

_USER_AGENT = "XMC-Review-Threads-Agent"
_SNIPPET_LENGTH = 120
_AUTH_INSTRUCTION = (
    "Authenticate using the 'gh' CLI (run 'gh auth login') or set the GITHUB_TOKEN"
    " environment variable, and verify the repository and PR number exist."
)


def _get_login() -> str:
    """Returns the authenticated user's GitHub login, as a CLI-facing error on failure."""
    try:
        return get_authenticated_login(user_agent=_USER_AGENT)
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to fetch the authenticated GitHub user: {e}", _AUTH_INSTRUCTION
        ) from e


def _snippet(body: str) -> str:
    """Collapses whitespace and truncates a comment body for display."""
    flat = " ".join(body.split())
    if len(flat) <= _SNIPPET_LENGTH:
        return flat
    return flat[: _SNIPPET_LENGTH - 3] + "..."


def summarize_thread(thread: dict[str, Any], login: str) -> dict[str, Any]:
    """Converts a raw GraphQL review thread node into a compact summary.

    Args:
        thread: Raw thread node returned by `list_review_threads`.
        login: Authenticated user's GitHub login.

    Returns:
        Summary with thread ID, `path:line` location, resolution state, whether
        `login` replied (authored any comment after the first), and a snippet of
        the first comment.
    """
    comments = (thread.get("comments") or {}).get("nodes") or []
    first = comments[0] if comments else {}
    first_author = (first.get("author") or {}).get("login", "unknown")
    replied_by_me = any(
        (comment.get("author") or {}).get("login") == login for comment in comments[1:]
    )
    return {
        "thread_id": thread.get("id"),
        "location": f"{thread.get('path') or 'Unknown'}:{thread.get('line') or '?'}",
        "is_resolved": bool(thread.get("isResolved")),
        "replied_by_me": replied_by_me,
        "first_comment_author": first_author,
        "first_comment_url": first.get("url", ""),
        "first_comment_snippet": _snippet(first.get("body") or ""),
    }


def _fetch_summaries(args: argparse.Namespace) -> tuple[str, list[dict[str, Any]]]:
    """Fetches all review threads of the PR and summarizes them for the current user."""
    login = _get_login()
    try:
        threads = list_review_threads(args.owner, args.repo, args.pr_number, user_agent=_USER_AGENT)
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(f"Failed to list review threads: {e}", _AUTH_INSTRUCTION) from e
    return login, [summarize_thread(thread, login) for thread in threads]


def list_threads(args: argparse.Namespace) -> dict[str, Any]:
    """Lists review threads on a PR with resolution and reply state."""
    login, summaries = _fetch_summaries(args)
    unresolved_count = sum(1 for s in summaries if not s["is_resolved"])
    if args.unresolved_only:
        summaries = [s for s in summaries if not s["is_resolved"]]
    return {
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "authenticated_user": login,
        "unresolved_count": unresolved_count,
        "threads": summaries,
    }


def _resolve_all(args: argparse.Namespace, candidates: list[dict[str, Any]]) -> dict[str, Any]:
    """Resolves each candidate thread, collecting per-thread outcomes."""
    resolved, failed = [], []
    for summary in candidates:
        try:
            resolve_review_thread(
                args.owner,
                args.repo,
                args.pr_number,
                summary["thread_id"],
                user_agent=_USER_AGENT,
            )
            resolved.append(summary["thread_id"])
        except (GithubAuthError, GithubAPIError) as e:
            failed.append({"thread_id": summary["thread_id"], "error": str(e)})
    return {"resolved": resolved, "failed": failed}


def resolve_replied(args: argparse.Namespace) -> dict[str, Any]:
    """Resolves unresolved review threads that the authenticated user replied to.

    Raises:
        HandledError: If any thread fails to resolve; details list the outcomes.
    """
    login, summaries = _fetch_summaries(args)
    candidates = [s for s in summaries if not s["is_resolved"] and s["replied_by_me"]]
    skipped = [s for s in summaries if not s["is_resolved"] and not s["replied_by_me"]]
    result: dict[str, Any] = {
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "authenticated_user": login,
        "dry_run": args.dry_run,
        "candidates": candidates,
        "skipped_unreplied": [s["thread_id"] for s in skipped],
    }
    if args.dry_run:
        return result

    result.update(_resolve_all(args, candidates))
    if result["failed"]:
        raise HandledError(
            f"Failed to resolve {len(result['failed'])} of {len(candidates)} review threads.",
            "Verify you have permission to resolve review threads, then re-run"
            " resolve-replied (already resolved threads are skipped).",
            details=result,
        )
    return result


def build_parser() -> argparse.ArgumentParser:
    """Builds the argument parser with `list` and `resolve-replied` subcommands."""
    parser = argparse.ArgumentParser(
        description="List PR review threads or resolve the ones you have replied to."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    list_parser = subparsers.add_parser(
        "list", help="List review threads with resolution and reply state."
    )
    list_parser.add_argument(
        "--unresolved-only", action="store_true", help="Only output unresolved threads."
    )
    list_parser.set_defaults(handler=list_threads)

    resolve_parser = subparsers.add_parser(
        "resolve-replied",
        help="Resolve unresolved threads that the authenticated user has replied to.",
    )
    resolve_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print the threads that would be resolved without resolving them.",
    )
    resolve_parser.set_defaults(handler=resolve_replied)

    for sub in (list_parser, resolve_parser):
        sub.add_argument("owner", help="GitHub repository owner (e.g. google).")
        sub.add_argument("repo", help="GitHub repository name (e.g. xmc).")
        sub.add_argument("pr_number", type=int, help="GitHub Pull Request number.")
    return parser


def main() -> None:
    """Main entrypoint for the review_threads tool."""
    run_tool(build_parser(), lambda args: args.handler(args))


if __name__ == "__main__":
    main()
