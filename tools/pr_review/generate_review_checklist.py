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

"""Tool to generate a PR review checklist markdown file from unresolved comments."""

import argparse
import sys
from collections.abc import Sequence
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
)


def _parse_graphql_nodes(nodes: Sequence[dict[str, Any] | None]) -> list[dict[str, Any]]:
    """Extracts unresolved comment items from GraphQL thread nodes."""
    items = []
    for thread in nodes:
        if not thread or thread.get("isResolved"):
            continue
        path = thread.get("path") or "Unknown"
        line = thread.get("line")
        comments_dict = thread.get("comments")
        comments_data = comments_dict.get("nodes", []) if comments_dict else []
        if not comments_data:
            continue
        first_comment = comments_data[0]
        if not first_comment:
            continue
        author_dict = first_comment.get("author")
        author = author_dict.get("login") if author_dict else "unknown"
        items.append(
            {
                "comment_id": first_comment.get("databaseId") or first_comment.get("id"),
                "author": author,
                "file_path": path,
                "line": line,
                "body": first_comment.get("body") or "",
                "url": first_comment.get("url") or "",
            }
        )
    return items


def _fetch_graphql_unresolved_comments(
    owner: str, repo: str, pr_number: int
) -> list[dict[str, Any]] | None:
    """Fetches unresolved comments using GitHub GraphQL API."""
    query = """
    query($owner: String!, $repo: String!, $pr_number: Int!, $cursor: String) {
      repository(owner: $owner, name: $repo) {
        pullRequest(number: $pr_number) {
          reviewThreads(first: 100, after: $cursor) {
            pageInfo {
              hasNextPage
              endCursor
            }
            nodes {
              isResolved
              path
              line
              comments(first: 50) {
                nodes {
                  id
                  databaseId
                  body
                  url
                  author {
                    login
                  }
                }
              }
            }
          }
        }
      }
    }
    """
    cursor = None
    all_items = []
    while True:
        variables = {
            "owner": owner,
            "repo": repo,
            "pr_number": pr_number,
            "cursor": cursor,
        }
        payload = {"query": query, "variables": variables}
        res = call_github_api("/graphql", method="POST", payload=payload)
        if not isinstance(res, dict):
            return None
        data = res.get("data")
        repository = data.get("repository") if data else None
        pull_request = repository.get("pullRequest") if repository else None
        if not pull_request:
            return None
        threads_info = pull_request.get("reviewThreads") or {}
        nodes = threads_info.get("nodes") or []
        all_items.extend(_parse_graphql_nodes(nodes))
        page_info = threads_info.get("pageInfo") or {}
        if not page_info.get("hasNextPage"):
            break
        cursor = page_info.get("endCursor")
    return all_items


def _parse_rest_comment(comment: dict[str, Any]) -> dict[str, Any]:
    """Converts a REST API comment object to a standardized dictionary."""
    user = comment.get("user")
    author = user.get("login") if user else "unknown"
    line = comment.get("line") or comment.get("original_line")
    return {
        "comment_id": comment.get("id"),
        "author": author,
        "file_path": comment.get("path") or "Unknown",
        "line": line,
        "body": comment.get("body") or "",
        "url": comment.get("html_url") or "",
    }


def _fetch_rest_comments(owner: str, repo: str, pr_number: int) -> list[dict[str, Any]]:
    """Fetches review comments using GitHub REST API."""
    comments = []
    page = 1
    while True:
        endpoint = f"/repos/{owner}/{repo}/pulls/{pr_number}/comments?per_page=100&page={page}"
        batch = call_github_api(endpoint, user_agent="XMC-Checklist-Generator")
        if not batch or not isinstance(batch, list):
            break
        for item in batch:
            if not item.get("in_reply_to_id"):
                comments.append(_parse_rest_comment(item))
        if len(batch) < 100:
            break
        page += 1
    return comments


def fetch_unresolved_comments(owner: str, repo: str, pr_number: int) -> list[dict[str, Any]]:
    """Fetches unresolved PR review comments via GraphQL API with REST fallback."""
    try:
        items = _fetch_graphql_unresolved_comments(owner, repo, pr_number)
        if items is not None:
            return items
    except Exception:
        pass

    try:
        return _fetch_rest_comments(owner, repo, pr_number)
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using the 'gh' CLI (run 'gh auth login') or"
            " set the GITHUB_TOKEN environment variable.",
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure the repository and PR number exist and that your"
            " credentials have access to the pull request.",
        )


def format_checklist_markdown(
    owner: str, repo: str, pr_number: int, comments: list[dict[str, Any]]
) -> str:
    """Formats unresolved review comments into a markdown checklist string."""
    lines = [
        f"# PR Review Checklist: {owner}/{repo}#{pr_number}",
        "",
        f"Total Unresolved Comments: {len(comments)}",
        "",
    ]

    if not comments:
        lines.append("No unresolved review comments found.")
        lines.append("")
        return "\n".join(lines)

    lines.append("## Unresolved Review Comments")
    lines.append("")
    for comment in comments:
        path = comment.get("file_path") or "Unknown"
        line = comment.get("line")
        url = comment.get("url") or "#"
        author = comment.get("author") or "unknown"
        body = comment.get("body") or ""

        location = f"{path}:{line}" if line is not None else path
        comment_id = comment.get("comment_id")
        comment_id_str = f" (Comment ID {comment_id})" if comment_id is not None else ""
        lines.append(f"- [ ] **[{location}]({url})** (@{author}){comment_id_str}")
        for body_line in body.strip().splitlines():
            lines.append(f"  > {body_line}")
        lines.append("")

    return "\n".join(lines)


def generate_review_checklist(args: argparse.Namespace) -> dict[str, Any]:
    """Orchestrates checklist generation from GitHub API and writes to file."""
    try:
        pr_num = int(args.pr_number)
    except ValueError:
        raise HandledError(
            f"Invalid PR number: {args.pr_number}",
            "Please provide a valid integer for the PR number.",
        )

    comments = fetch_unresolved_comments(args.owner, args.repo, pr_num)
    markdown_content = format_checklist_markdown(args.owner, args.repo, pr_num, comments)

    output_path = args.output
    try:
        Path(output_path).write_text(markdown_content, encoding="utf-8")
    except OSError as e:
        raise HandledError(
            f"Failed to write output file '{output_path}': {str(e)}",
            "Ensure parent directories exist and you have write permissions.",
        )

    return {
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": pr_num,
        "output_file": output_path,
        "unresolved_comment_count": len(comments),
    }


def main():
    """Main entrypoint for generate_review_checklist tool."""
    parser = argparse.ArgumentParser(
        description="Fetch unresolved review comments and generate pr_review_checklist.md."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("pr_number", help="GitHub Pull Request number.")
    parser.add_argument(
        "-o",
        "--output",
        default="pr_review_checklist.md",
        help="Output markdown file path (default: pr_review_checklist.md).",
    )
    run_tool(parser, generate_review_checklist)


if __name__ == "__main__":
    main()
