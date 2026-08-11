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

"""Helper CLI script to generate PR review audit checklists and existing comment indexes."""

import argparse
import os
import re
from pathlib import Path
from typing import Any

from github_utils import GithubAPIError, GithubAuthError, call_github_api
from tool_utils import HandledError, run_tool


def extract_task_references(description: str) -> list[str]:
    """Extract Buganizer (b/12345) and issue references from description."""
    if not description:
        return []
    bugs = re.findall(r"\bb/\d+\b", description, re.IGNORECASE)
    issues = re.findall(r"https?://github\.com/[^/\s]+/[^/\s]+/(?:issues|pull)/\d+", description)
    return sorted(set(bugs + issues))


def resolve_output_path(output_arg: str, conv_id: str, default_filename: str) -> Path:
    """Resolve the output path, defaulting to conversation artifact folder if conv_id provided."""
    if output_arg:
        return Path(output_arg)
    if conv_id:
        base_dir = Path(
            os.environ.get(
                "JETSKI_ARTIFACT_DIR",
                f"/usr/local/google/home/vluk/.gemini/jetski/brain/{conv_id}",
            )
        )
        base_dir.mkdir(parents=True, exist_ok=True)
        return base_dir / default_filename
    return Path(default_filename)


def fetch_pr_audit_data(owner: str, repo: str, pr_number: int) -> dict[str, Any]:
    """Fetch PR details, files, and existing review comments from GitHub API."""
    try:
        pr_info = call_github_api(
            f"/repos/{owner}/{repo}/pulls/{pr_number}",
            user_agent="XMC-PR-Review-Agent",
        )
        files = call_github_api(
            f"/repos/{owner}/{repo}/pulls/{pr_number}/files",
            user_agent="XMC-PR-Review-Agent",
        )
        comments = call_github_api(
            f"/repos/{owner}/{repo}/pulls/{pr_number}/comments",
            user_agent="XMC-PR-Review-Agent",
        )
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to fetch PR details for {owner}/{repo}#{pr_number}: {e}",
            "Verify GitHub CLI authentication ('gh auth login') or GITHUB_TOKEN environment variable.",
        ) from e

    return {
        "pr_info": pr_info,
        "files": files if isinstance(files, list) else [],
        "comments": comments if isinstance(comments, list) else [],
    }


def format_initial_audit_markdown(
    owner: str, repo: str, pr_number: int, data: dict[str, Any]
) -> str:
    """Format the initial review audit checklist markdown artifact."""
    pr = data["pr_info"]
    files = data["files"]
    comments = data["comments"]

    title = pr.get("title", "")
    body = pr.get("body", "") or "No description provided."
    head_sha = pr.get("head", {}).get("sha", "unknown")
    base_branch = pr.get("base", {}).get("ref", "main")
    head_branch = pr.get("head", {}).get("ref", "unknown")
    task_refs = extract_task_references(body)

    task_ref_section = (
        "\n".join(f"- Reference: `{ref}`" for ref in task_refs)
        if task_refs
        else "- None detected in PR description"
    )

    file_list_section = "\n".join(
        f"- `{f.get('filename')}` (+{f.get('additions', 0)} / -{f.get('deletions', 0)})"
        for f in files
    )

    # Summarize existing inline comments to prevent duplication
    if comments:
        existing_threads = []
        for c in comments:
            cid = c.get("id")
            path = c.get("path")
            line = c.get("line") or c.get("original_line")
            user = c.get("user", {}).get("login", "unknown")
            c_body = (c.get("body", "") or "").replace("\n", " ")
            if len(c_body) > 80:
                c_body = c_body[:77] + "..."
            existing_threads.append(
                f"- **Comment ID `{cid}`** | File: `{path}` | Line: `{line}` | Author: `{user}` | Text: *{c_body}*"
            )
        existing_section = "\n".join(existing_threads)
    else:
        existing_section = "- No existing inline review comments found on this PR."

    return f"""# PR Review Audit Checklist & Direction Plan

## Pull Request Overview
- **Repository**: `{owner}/{repo}`
- **PR Number**: #{pr_number}
- **Title**: {title}
- **Base / Head**: `{base_branch}` ← `{head_branch}` (`{head_sha}`)
- **Task / Bug References**:
{task_ref_section}

---

## PR Description (Requirement Analysis)
```markdown
{body}
```

> [!NOTE]
> **Description vs Code Verification**: Reviewers must reconcile the actual file modifications below against the PR title, description, and task references above.

---

## Touchpoints & Modified Files ({len(files)} files)
{file_list_section}

---

## Existing Inline Review Comments (Deduplication Index)
*Do NOT propose duplicate inline comments for issues already raised below:*
{existing_section}

---

## User Direction Window & Custom Review Guidance
*Document reviewer-proposed directions, priority modules, or initial inline comments below:*
- [ ] Review PR title and description reconciliation against actual file delta
- [ ] Check guidelines for touched file extensions (`golang.md`, `python.md`, `core_engineering.md`, etc.)

---

## Proposed Inline Review Comments (Confirmation Checklist)
*Populate proposed inline comments below during Phase 3 review synthesis. All proposed items require explicit user confirmation prior to posting:*

| Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- |
| #- | Draft | `pkg/example/example.go` | `10` | `core_engineering.md` | *Sample proposed comment* |
"""


def format_rereview_audit_markdown(
    owner: str, repo: str, pr_number: int, data: dict[str, Any]
) -> str:
    """Format the follow-up re-review audit report markdown artifact."""
    pr = data["pr_info"]
    comments = data["comments"]
    title = pr.get("title", "")
    head_sha = pr.get("head", {}).get("sha", "unknown")

    # Group comment threads
    root_comments = [c for c in comments if not c.get("in_reply_to_id")]
    replies_by_root: dict[int, list[dict[str, Any]]] = {}
    for c in comments:
        reply_to = c.get("in_reply_to_id")
        if reply_to:
            replies_by_root.setdefault(reply_to, []).append(c)

    thread_blocks = []
    for root in root_comments:
        cid = root.get("id")
        path = root.get("path")
        line = root.get("line") or root.get("original_line")
        root_author = root.get("user", {}).get("login", "unknown")
        root_text = root.get("body", "")

        thread_replies = replies_by_root.get(cid, [])
        latest_reply = thread_replies[-1] if thread_replies else None
        latest_author = (
            latest_reply.get("user", {}).get("login", "none") if latest_reply else "none"
        )
        latest_text = latest_reply.get("body", "") if latest_reply else "None"

        thread_blocks.append(
            f"""### Thread ID `{cid}` (`{path}`:L{line})
- **Original Reviewer (`{root_author}`)**:
  > {root_text}
- **Latest Reply (`{latest_author}`)**:
  > {latest_text}
- **Audit Verification Status**: `- [ ] Pending Analysis`
- **Recommended Action**: `[RESOLVE]` or `[RESPOND]`
- **Proposed Counter-Reply / Rationale**:
  *Leave blank if recommending [RESOLVE], or specify proposed comment below:*

  ```markdown
  Addressed verification check for line {line}.
  ```
---"""
        )

    threads_section = (
        "\n".join(thread_blocks)
        if thread_blocks
        else "No inline review threads found to re-review."
    )

    return f"""# PR Re-Review Audit Report & Thread Resolution Plan

## Pull Request Context
- **PR**: `{owner}/{repo}` #{pr_number} - *{title}*
- **Latest Head SHA**: `{head_sha}`

> [!IMPORTANT]
> **Instructions for Reviewer Iteration**:
> 1. Review each thread verification status below.
> 2. You may edit **Recommended Action** (`[RESOLVE]` or `[RESPOND]`) and **Proposed Counter-Reply / Rationale** inline in this markdown file.
> 3. Once satisfied, confirm to batch-apply resolutions or post inline counter-replies.

---

## Review Thread Re-Evaluation Matrix ({len(root_comments)} threads)

{threads_section}
"""


def main(args: argparse.Namespace) -> dict[str, Any]:
    """Execute PR audit checklist generation."""
    data = fetch_pr_audit_data(args.owner, args.repo, args.pr_number)

    if args.mode == "re-review":
        default_filename = "pr_rereview_audit_report.md"
        content = format_rereview_audit_markdown(args.owner, args.repo, args.pr_number, data)
    else:
        default_filename = "pr_review_audit_checklist.md"
        content = format_initial_audit_markdown(args.owner, args.repo, args.pr_number, data)

    out_path = resolve_output_path(args.output, args.conv_id, default_filename)
    out_path.write_text(content, encoding="utf-8")

    return {
        "status": "success",
        "mode": args.mode,
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "output_file": str(out_path.resolve()),
        "file_count": len(data["files"]),
        "existing_comment_count": len(data["comments"]),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Generate PR review audit checklist and existing comment indexes."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("pr_number", type=int, help="GitHub Pull Request number.")
    parser.add_argument(
        "--mode",
        choices=["initial", "re-review"],
        default="initial",
        help="Audit mode: 'initial' for fresh review or 're-review' for author reply audit.",
    )
    parser.add_argument("-o", "--output", help="Explicit output path for generated markdown.")
    parser.add_argument(
        "--conv-id",
        help="Conversation ID for storing artifact inside brain directory by default.",
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), main)
