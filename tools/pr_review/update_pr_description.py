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

"""Helper script to update Pull Request body/description on GitHub."""

import argparse
import re
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.pr_review.github_utils import (  # noqa: E402
    GithubAPIError,
    GithubAuthError,
    call_github_api,
)


def parse_pr_identifier(pr_arg: str):
    """Parses PR argument into owner, repo, and pull_number."""
    if pr_arg.isdigit():
        return {"owner": "google", "repo": "xmc", "pull_number": int(pr_arg)}

    match_url = re.search(r"github\.com/([^/]+)/([^/]+)/pull/(\d+)", pr_arg)
    if match_url:
        return {
            "owner": match_url.group(1),
            "repo": match_url.group(2),
            "pull_number": int(match_url.group(3)),
        }

    raise HandledError(
        f"Invalid PR identifier: '{pr_arg}'",
        "Provide either a numeric PR ID (e.g. '148') or a full GitHub PR URL"
        " (e.g. 'https://github.com/google/xmc/pull/148').",
    )


def update_pr_description(args):
    """Updates the body/description of a GitHub Pull Request via REST API."""
    parsed = parse_pr_identifier(args.pr)
    owner = parsed["owner"]
    repo = parsed["repo"]
    pull_number = parsed["pull_number"]

    body_content = ""
    if args.body_file:
        body_path = Path(args.body_file)
        if not body_path.is_file():
            raise HandledError(
                f"Body file not found or is not a regular file: '{args.body_file}'",
                "Ensure the specified body markdown file exists and is a regular file.",
            )
        body_content = body_path.read_text(encoding="utf-8")
    elif args.body:
        body_content = args.body
    else:
        raise HandledError(
            "Neither --body nor --body-file was provided.",
            "Pass the PR description using --body-file <path> or --body <text>.",
        )

    endpoint = f"/repos/{owner}/{repo}/pulls/{pull_number}"
    payload = {"body": body_content}

    try:
        response = call_github_api(
            endpoint,
            method="PATCH",
            payload=payload,
            user_agent="XMC-PR-Updater-Agent",
        )
        return {
            "status": "success",
            "pull_number": pull_number,
            "url": response.get("html_url", ""),
            "updated_body_length": len(body_content),
        }
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using 'gh auth login' or set the GITHUB_TOKEN"
            " environment variable.",
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure the PR exists and your credentials have write access permissions.",
        )


def main():
    """Main entrypoint for update_pr_description tool."""
    parser = argparse.ArgumentParser(description="Update Pull Request description/body on GitHub.")
    parser.add_argument(
        "pr",
        help="Pull Request number (e.g. '148') or full GitHub PR URL.",
    )
    parser.add_argument(
        "--body-file",
        help="Path to markdown file containing the new PR description.",
    )
    parser.add_argument(
        "--body",
        help="Inline string content for the new PR description.",
    )
    run_tool(parser, update_pr_description)


if __name__ == "__main__":
    main()
