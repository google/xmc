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

"""Helper script to post a reply to a PR comment on GitHub."""

import argparse
import json
import subprocess
from datetime import datetime, timezone

from github_utils import (
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    parse_comment_url,
)
from tool_utils import HandledError, run_tool


def reply_comment(args):
    """Posts a reply to a GitHub PR comment thread with telemetry data."""
    comment_url = args.comment_url
    if args.message_file:
        with open(args.message_file, encoding="utf-8") as f:
            message = f.read().strip()
    else:
        message = args.message

    parsed = parse_comment_url(comment_url)
    if not parsed or parsed["type"] != "review_comment":
        raise HandledError(
            "Could not parse Pull Request review comment URL.",
            "Ensure the URL matches Github PR review comment anchor patterns"
            " (e.g."
            " 'https://github.com/owner/repo/pull/num#discussion_r123456').",
        )

    # Fetch authenticated user's Github login
    try:
        user_info = call_github_api("/user", user_agent="XMC-Comment-Replier-Agent")
        github_username = user_info.get("login", "unknown")
    except Exception:
        github_username = "unknown"

    # Fetch latest commit ID of the SKILL.md file
    try:
        skill_commit_id = subprocess.check_output(
            [
                "git",
                "log",
                "-1",
                "--format=%H",
                "--",
                ":/.agents/skills/xmc_address_pr_comment/SKILL.md",
            ],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        if not skill_commit_id:
            skill_commit_id = "unknown"
    except Exception:
        skill_commit_id = "unknown"

    # Construct telemetry payload
    now = datetime.now(timezone.utc)
    telemetry = {
        "git_username": github_username,
        "date": now.strftime("%Y-%m-%d"),
        "time": now.strftime("%H:%M:%S UTC"),
        "pr_number": parsed["pull_number"],
        "comment_id": parsed["comment_id"],
        "skill_name": "xmc_address_pr_comment",
        "skill_commit_id": skill_commit_id,
    }

    # Append to body as a hidden HTML comment
    serialized_telemetry = json.dumps(telemetry)
    full_message = f"{message}\n\n<!-- xmc-telemetry: {serialized_telemetry} -->"

    # Replying to a specific review comment in a thread
    owner = parsed["owner"]
    repo = parsed["repo"]
    pull_number = parsed["pull_number"]
    endpoint = f"/repos/{owner}/{repo}/pulls/{pull_number}/comments"
    payload = {"body": full_message, "in_reply_to": parsed["comment_id"]}

    try:
        return call_github_api(
            endpoint,
            method="POST",
            payload=payload,
            user_agent="XMC-Comment-Replier-Agent",
        )
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
            " credentials have write access permissions.",
        )


def main():
    """Main entrypoint for reply_pr_comment tool."""
    parser = argparse.ArgumentParser(
        description="Post a reply to a Pull Request review comment thread."
    )
    parser.add_argument("comment_url", help="The URL of the GitHub comment to reply to.")
    parser.add_argument(
        "message",
        nargs="?",
        default="code changed",
        help="The message to post as a reply. Defaults to 'code changed'.",
    )
    parser.add_argument(
        "--message-file",
        "-f",
        dest="message_file",
        help="Path to a text file containing the reply message.",
    )
    run_tool(parser, reply_comment)


if __name__ == "__main__":
    main()
