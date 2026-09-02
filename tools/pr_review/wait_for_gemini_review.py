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

"""Waits dynamically for gemini-code-assist review completion on a GitHub Pull Request."""

import argparse
import json
import subprocess
import sys
import time
from typing import Any

BOT_LOGINS = {"gemini-code-assist", "gemini-code-assist[bot]"}


def run_gh_json(cmd: list[str]) -> Any:
    """Executes a GitHub CLI command and parses JSON output."""
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return json.loads(res.stdout) if res.stdout.strip() else None
    except (subprocess.CalledProcessError, json.JSONDecodeError):
        return None


def get_latest_review_trigger_time(owner: str, repo: str, pr_number: int) -> str | None:
    """Finds the timestamp of the latest '/gemini review' comment on the PR."""
    try:
        res = subprocess.run(
            [
                "gh",
                "api",
                f"repos/{owner}/{repo}/issues/{pr_number}/comments",
                "--jq",
                '[.[] | select(.body | test("/gemini review"))] | reverse | .[0].created_at // empty',
            ],
            capture_output=True,
            text=True,
            check=True,
        )
        val = res.stdout.strip()
        return val if val else None
    except subprocess.CalledProcessError:
        return None


def get_matching_review(
    owner: str, repo: str, pr_number: int, since_timestamp: str | None
) -> dict[str, Any] | None:
    """Checks if a review from gemini-code-assist has been submitted since the given timestamp."""
    reviews = run_gh_json(
        [
            "gh",
            "api",
            f"repos/{owner}/{repo}/pulls/{pr_number}/reviews",
        ]
    )
    if not isinstance(reviews, list):
        return None

    for r in reversed(reviews):
        user_login = r.get("user", {}).get("login", "")
        submitted_at = r.get("submitted_at")
        if user_login in BOT_LOGINS and submitted_at:
            if since_timestamp is None or submitted_at >= since_timestamp:
                return {
                    "id": r.get("id"),
                    "user": user_login,
                    "submitted_at": submitted_at,
                    "state": r.get("state"),
                    "body": r.get("body", ""),
                    "html_url": r.get("html_url", ""),
                }
    return None


def wait_for_gemini_review(
    owner: str,
    repo: str,
    pr_number: int,
    timeout_seconds: int = 600,
    interval_seconds: int = 15,
    since_timestamp: str | None = None,
) -> dict[str, Any]:
    """Polls until gemini-code-assist posts a review summary or timeout expires."""
    since = since_timestamp or get_latest_review_trigger_time(owner, repo, pr_number)
    start_time = time.time()

    while time.time() - start_time < timeout_seconds:
        review = get_matching_review(owner, repo, pr_number, since)
        if review:
            review["elapsed_seconds"] = round(time.time() - start_time, 2)
            return {"status": "success", "result": review}
        time.sleep(interval_seconds)

    elapsed = round(time.time() - start_time, 2)
    return {
        "status": "error",
        "message": (
            f"Timed out after {elapsed}s waiting for gemini-code-assist review on "
            f"{owner}/{repo}#{pr_number} (since {since or 'start'})."
        ),
    }


def main() -> int:
    """CLI entrypoint for waiting on gemini-code-assist PR review completion."""
    parser = argparse.ArgumentParser(
        description="Wait dynamically for gemini-code-assist review completion on a PR."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. 'google')")
    parser.add_argument("repo", help="GitHub repository name (e.g. 'xmc')")
    parser.add_argument("pr_number", type=int, help="Pull request number")
    parser.add_argument(
        "--timeout",
        type=int,
        default=600,
        help="Maximum seconds to wait before timing out (default: 600s / 10m)",
    )
    parser.add_argument(
        "--interval",
        type=int,
        default=15,
        help="Seconds between API polling checks (default: 15s)",
    )
    parser.add_argument(
        "--since",
        type=str,
        default=None,
        help="ISO 8601 timestamp cutoff. If omitted, uses latest '/gemini review' comment time.",
    )

    args = parser.parse_args()
    result = wait_for_gemini_review(
        owner=args.owner,
        repo=args.repo,
        pr_number=args.pr_number,
        timeout_seconds=args.timeout,
        interval_seconds=args.interval,
        since_timestamp=args.since,
    )
    print(json.dumps(result, indent=2))
    return 0 if result["status"] == "success" else 1


if __name__ == "__main__":
    sys.exit(main())
