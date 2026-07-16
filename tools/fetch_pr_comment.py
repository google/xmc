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

"""Helper script to retrieve comment details from GitHub API."""

import argparse

from github_utils import (
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    parse_comment_url,
)
from tool_utils import HandledError, run_tool


def fetch_comment(args):
    """Fetches GitHub review comment details given a comment URL."""
    comment_url = args.comment_url
    parsed = parse_comment_url(comment_url)
    if not parsed or parsed["type"] != "review_comment":
        raise HandledError(
            "Could not parse Pull Request review comment URL.",
            "Ensure the URL matches Github PR review comment anchor patterns"
            " (e.g."
            " 'https://github.com/owner/repo/pull/num#discussion_r123456').",
        )

    try:
        owner = parsed["owner"]
        repo = parsed["repo"]
        comment_id = parsed["comment_id"]
        endpoint = f"/repos/{owner}/{repo}/pulls/comments/{comment_id}"
        data = call_github_api(
            endpoint, user_agent="XMC-Comment-Fetcher-Agent"
        )

        return {
            "type": "review_comment",
            "comment_id": data.get("id"),
            "author": data.get("user", {}).get("login"),
            "body": data.get("body"),
            "file_path": data.get("path"),
            "line": data.get("line") or data.get("original_line"),
            "diff_hunk": data.get("diff_hunk"),
            "commit_id": data.get("commit_id"),
        }
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using the 'gh' CLI (run 'gh auth login') or"
            " set the GITHUB_TOKEN environment variable.",
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure the repository and comment ID exist, and that your"
            " credentials have appropriate permissions to access the comments.",
        )


def main():
    """Main entrypoint for fetch_pr_comment tool."""
    parser = argparse.ArgumentParser(
        description=(
            "Retrieve comment details (author, body, file path, line, diff hunk,"
            " commit id) from a GitHub Pull Request review comment URL."
        )
    )
    parser.add_argument(
        "comment_url", help="The URL of the GitHub PR review comment."
    )
    run_tool(parser, fetch_comment)


if __name__ == "__main__":
    main()
