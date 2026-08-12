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

"""Helper CLI script to verify that 100% of approved PR review comments were posted inline to GitHub."""

import argparse
import re
from pathlib import Path
from typing import Any

from github_utils import GithubAPIError, GithubAuthError, call_github_api
from tool_utils import HandledError, run_tool


def parse_checklist_approved_items(
    content: str,
) -> list[dict[str, Any]]:
    """Parse approved comment or resolution items from the markdown audit artifact."""
    items = []

    # Case 1: Initial review checklist table rows:
    # | Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
    table_pattern = re.compile(
        r"\|\s*([^\s|]+)\s*\|\s*(Approved|Confirmed|Posted)\s*\|\s*`?([^`|]+)`?\s*\|\s*`?(\d+)`?\s*\|\s*([^|]+)\s*\|\s*([^|]+)\s*\|",
        re.IGNORECASE,
    )
    for match in table_pattern.finditer(content):
        item_id, status, file_path, line, rule, proposed = match.groups()
        items.append(
            {
                "type": "initial_inline",
                "item_id": item_id.strip(),
                "status": status.strip(),
                "file_path": file_path.strip(),
                "line": int(line.strip()),
                "rule": rule.strip(),
                "proposed": proposed.strip(),
            }
        )

    # Case 2: Re-review thread blocks with Recommended Action [RESPOND] or [RESOLVE]
    thread_pattern = re.compile(
        r"###\s+Thread ID\s+`(\d+)`\s+\(`([^`]+)`:L(\d+)\)(?:(?!###|---).)*?Recommended Action[^`:]*[:`]*\s*`?\[(RESOLVE|RESPOND)[^`\]]*\]`?",
        re.DOTALL,
    )
    for match in thread_pattern.finditer(content):
        thread_id, file_path, line, action = match.groups()
        items.append(
            {
                "type": "re_review_thread",
                "thread_id": int(thread_id),
                "file_path": file_path.strip(),
                "line": int(line),
                "action": action.upper(),
            }
        )

    return items


def verify_review_comments_posted(args: argparse.Namespace) -> dict[str, Any]:
    """Verify approved audit checklist items against actual posted inline GitHub comments."""
    checklist_path = Path(args.checklist_file)
    if not checklist_path.is_file():
        raise HandledError(
            f"Audit checklist file '{args.checklist_file}' does not exist.",
            "Ensure the checklist artifact file path is valid and present in the conversation artifact directory.",
        )

    content = checklist_path.read_text(encoding="utf-8")
    items = parse_checklist_approved_items(content)

    if not items:
        # Check if there are any unchecked - [ ] interactive checklist items in general
        raw_checkboxes = re.findall(r"-\s*\[\s*\]\s*(.*)", content)
        return {
            "status": "warning",
            "message": "No explicit approved table rows or thread re-review actions detected.",
            "raw_open_checkbox_count": len(raw_checkboxes),
            "total_checklist_items": 0,
            "posted_items": 0,
            "unposted_items": [],
        }

    try:
        live_comments = call_github_api(
            f"/repos/{args.owner}/{args.repo}/pulls/{args.pr_number}/comments",
            user_agent="XMC-PR-Review-Agent",
        )
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to fetch live GitHub comments for PR #{args.pr_number}: {e}",
            "Check GitHub API connectivity and authentication credentials.",
        ) from e

    if not isinstance(live_comments, list):
        live_comments = []

    unposted = []
    verified_count = 0

    for item in items:
        if item["type"] == "initial_inline":
            # Match by path and line
            found = False
            for lc in live_comments:
                lc_path = lc.get("path")
                lc_line = lc.get("line") or lc.get("original_line")
                if lc_path == item["file_path"] and str(lc_line) == str(item["line"]):
                    found = True
                    break
            if found:
                verified_count += 1
            else:
                unposted.append(
                    f"Approved comment '{item['item_id']}' on {item['file_path']}:L{item['line']} missing from live PR diff"
                )

        elif item["type"] == "re_review_thread":
            thread_id = item["thread_id"]
            if item["action"] == "RESPOND":
                # Check that at least one reply exists where in_reply_to_id == thread_id
                replies = [lc for lc in live_comments if lc.get("in_reply_to_id") == thread_id]
                if replies:
                    verified_count += 1
                else:
                    unposted.append(
                        f"Recommended response on Thread ID `{thread_id}` has not been posted"
                    )
            elif item["action"] == "RESOLVE":
                is_checked = False
                thread_header = f"### Thread ID `{thread_id}`"
                if thread_header in content:
                    start_idx = content.find(thread_header)
                    next_thread_idx = content.find("### Thread ID", start_idx + len(thread_header))
                    thread_block = (
                        content[start_idx:next_thread_idx]
                        if next_thread_idx != -1
                        else content[start_idx:]
                    )
                    is_checked = "- **Audit Verification Status**: `- [x]" in thread_block
                has_reply = any(lc.get("in_reply_to_id") == thread_id for lc in live_comments)
                if is_checked or has_reply:
                    verified_count += 1
                else:
                    unposted.append(
                        f"RESOLVE action on Thread ID `{thread_id}` has not been verified (neither marked [x] nor has a reply)"
                    )

    # Update checklist file marking approved items as verified/posted
    updated_content = content
    for item in items:
        if item["type"] == "initial_inline":
            pattern = re.compile(
                rf"(\|\s*{re.escape(item['item_id'])}\s*\|\s*)(Approved|Confirmed)(\s*\|)",
                re.IGNORECASE,
            )
            updated_content = pattern.sub(r"\1Posted\3", updated_content)

    checklist_path.write_text(updated_content, encoding="utf-8")

    if unposted:
        raise HandledError(
            f"Review Comment Verification Failed: {len(unposted)} approved item(s) were not found on GitHub.",
            "Unposted items:\n"
            + "\n".join(f"- {u}" for u in unposted)
            + "\n\nExecute review_pr_post_inline_comment.py for missing items before finalizing review.",
        )

    return {
        "status": "success",
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "checklist_file": str(checklist_path.resolve()),
        "total_checklist_items": len(items),
        "posted_items": verified_count,
        "unposted_items": [],
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Verify programmatically that 100% of approved PR review comments were posted inline to GitHub."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("pr_number", type=int, help="GitHub Pull Request number.")
    parser.add_argument(
        "-c",
        "--checklist-file",
        required=True,
        help="Path to the audit checklist artifact file in conversation directory.",
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), verify_review_comments_posted)
