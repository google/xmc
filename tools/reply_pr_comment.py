#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to post a reply to a PR comment on GitHub

import argparse
import json
import subprocess
from datetime import datetime, timezone
from tool_utils import run_tool, HandledError
from github_utils import parse_comment_url, call_github_api, GithubAuthError, GithubAPIError

def reply_comment(args):
    comment_url = args.comment_url
    message = args.message
    
    parsed = parse_comment_url(comment_url)
    if not parsed or parsed["type"] != "review_comment":
        raise HandledError(
            "Could not parse Pull Request review comment URL.",
            "Ensure the URL matches Github PR review comment anchor patterns (e.g. 'https://github.com/owner/repo/pull/num#discussion_r123456')."
        )
        
    # Fetch authenticated user's Github login
    try:
        user_info = call_github_api("/user", user_agent="XMC-Comment-Replier-Agent")
        github_username = user_info.get("login", "unknown")
    except Exception:
        github_username = "unknown"

    # Fetch latest commit ID of the SKILL.md file (using :/ to resolve from repo root)
    try:
        skill_commit_id = subprocess.check_output(
            ["git", "log", "-1", "--format=%H", "--", ":/.agents/skills/address_pr_comment/SKILL.md"],
            text=True,
            stderr=subprocess.DEVNULL
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
        "skill_name": "address_pr_comment",
        "skill_commit_id": skill_commit_id
    }
    
    # Append to body as a hidden HTML comment
    serialized_telemetry = json.dumps(telemetry)
    full_message = f"{message}\n\n<!-- xmc-telemetry: {serialized_telemetry} -->"
        
    # Replying to a specific review comment in a thread
    endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/pulls/{parsed['pull_number']}/comments"
    payload = {
        "body": full_message,
        "in_reply_to": parsed["comment_id"]
    }
        
    try:
        response = call_github_api(endpoint, method="POST", payload=payload, user_agent="XMC-Comment-Replier-Agent")
        return response
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using the 'gh' CLI (run 'gh auth login') or set the GITHUB_TOKEN environment variable."
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure the repository and comment ID exist, and that your credentials have write access permissions."
        )

def main():
    parser = argparse.ArgumentParser(
        description="Post a reply to a Pull Request review comment thread."
    )
    parser.add_argument("comment_url", help="The URL of the GitHub comment to reply to.")
    parser.add_argument("message", nargs="?", default="code changed", help="The message to post as a reply. Defaults to 'code changed'.")
    run_tool(parser, reply_comment)

if __name__ == '__main__':
    main()
