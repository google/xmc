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

"""Helper script to verify 1-to-1 PR review comment thread resolution."""

import argparse
import os
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


def verify_resolution(args):
    """Verifies that all comment IDs in a checklist have received inline replies."""
    owner = args.owner
    repo = args.repo
    pull_number = args.pull_number
    checklist_path = args.checklist_file

    if not os.path.exists(checklist_path):
        raise HandledError(
            f"Checklist file '{checklist_path}' does not exist.",
            "Ensure you have created the Consolidated Review Checklist artifact"
            " before running resolution verification.",
        )

    try:
        content = Path(checklist_path).read_text(encoding="utf-8")
    except Exception as e:
        raise HandledError(
            f"Failed to read checklist file '{checklist_path}': {e}",
            "Check file permissions and ensure it is readable Markdown.",
        )

    checklist_ids = [int(x) for x in re.findall(r"Comment ID [`'\"]?(\d+)[`'\"]?", content)]
    if not checklist_ids:
        raise HandledError(
            f"No valid comment IDs found in checklist file '{checklist_path}'.",
            "Verify the checklist contains formatted items like 'Comment ID `12345`'.",
        )

    endpoint = f"/repos/{owner}/{repo}/pulls/{pull_number}/comments?per_page=100"
    try:
        comments = call_github_api(endpoint, user_agent="XMC-Comment-Verifier-Agent")
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using 'gh auth login' or set GITHUB_TOKEN.",
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure repository and PR number exist and credentials have read permissions.",
        )

    if not isinstance(comments, list):
        comments = []

    replied_to_ids = {
        c.get("in_reply_to_id") for c in comments if isinstance(c, dict) and c.get("in_reply_to_id")
    }

    unresolved_ids = [cid for cid in checklist_ids if cid not in replied_to_ids]
    resolved_ids = [cid for cid in checklist_ids if cid in replied_to_ids]

    if unresolved_ids:
        raise HandledError(
            f"1-to-1 Resolution Verification Failed: {len(unresolved_ids)} of"
            f" {len(checklist_ids)} comment threads lack inline replies."
            f" Unresolved comment IDs: {unresolved_ids}",
            "Post inline replies for all unresolved comment IDs using"
            " 'tools/pr_review/reply_pr_comment.py <comment_url> <message>' before marking task complete.",
        )

    return {
        "status": "success",
        "total_checklist_items": len(checklist_ids),
        "resolved_items": len(resolved_ids),
        "unresolved_ids": [],
        "message": (
            f"Verified 1-to-1 thread resolution: All {len(checklist_ids)}"
            " review comment threads have inline replies."
        ),
    }


def main():
    """Main entrypoint for verify_pr_comment_resolution tool."""
    parser = argparse.ArgumentParser(
        description=(
            "Verify 1-to-1 PR review comment thread resolution against a checklist artifact."
        )
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. 'google').")
    parser.add_argument("repo", help="GitHub repository name (e.g. 'xmc').")
    parser.add_argument("pull_number", type=int, help="Pull request number (e.g. 145).")
    parser.add_argument(
        "checklist_file",
        help="Path to review checklist file (e.g. 'pr_review_checklist.md').",
    )
    run_tool(parser, verify_resolution)


if __name__ == "__main__":
    main()
