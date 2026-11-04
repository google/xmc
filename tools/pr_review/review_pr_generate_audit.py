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
import collections
import json
import os
import re
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
    call_github_api,
    list_all_pages,
    list_review_threads,
)
from tools.pr_review.review_pr_workspace import (  # noqa: E402
    DEFAULT_FULL_REVIEW_THRESHOLD,
    measure_rereview_delta,
    prepare_review_workspace,
)


def extract_task_references(description: str | None) -> list[str]:
    """Extract task and issue references from description."""
    if not description:
        return []
    tasks = re.findall(r"\b(?:task|issue|bug)\s*#?\d+\b", description, re.IGNORECASE)
    issues = re.findall(r"https?://github\.com/[^/\s]+/[^/\s]+/(?:issues|pull)/\d+", description)
    return sorted(set(tasks + issues))


def resolve_output_path(
    output_arg: str, conv_id: str, default_filename: str, out_dir: str | None = None
) -> Path:
    """Resolve the output path: explicit path, then `out_dir`, then the conversation folder."""
    if output_arg:
        return Path(output_arg)
    if out_dir:
        Path(out_dir).mkdir(parents=True, exist_ok=True)
        return Path(out_dir) / default_filename
    if conv_id:
        env_dir = os.environ.get("ARTIFACT_DIR") or os.environ.get("JETSKI_ARTIFACT_DIR")
        if env_dir:
            base_dir = Path(env_dir)
        else:
            default_brain = Path.home() / ".gemini" / "jetski" / "brain" / conv_id
            if default_brain.parent.exists():
                base_dir = default_brain
            else:
                base_dir = Path.cwd() / ".review_artifacts" / conv_id
        base_dir.mkdir(parents=True, exist_ok=True)
        return base_dir / default_filename
    return Path(default_filename)


def fetch_pr_audit_data(owner: str, repo: str, pr_number: int) -> dict[str, Any]:
    """Fetch PR details, files, existing review comments, and review threads from GitHub.

    A review-thread failure does not fail the audit: `threads` is then empty and
    `threads_error` carries the error, so all threads are treated as open.
    """
    pr_endpoint = f"/repos/{owner}/{repo}/pulls/{pr_number}"
    try:
        pr_info = call_github_api(pr_endpoint, user_agent="XMC-PR-Review-Agent")
        files = list_all_pages(f"{pr_endpoint}/files", user_agent="XMC-PR-Review-Agent")
        comments = list_all_pages(f"{pr_endpoint}/comments", user_agent="XMC-PR-Review-Agent")
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to fetch PR details for {owner}/{repo}#{pr_number}: {e}",
            "Verify GitHub CLI authentication ('gh auth login') or GITHUB_TOKEN environment variable.",
        ) from e

    threads, threads_error = _fetch_review_threads(owner, repo, pr_number)
    data: dict[str, Any] = {
        "pr_info": pr_info,
        "files": files,
        "comments": comments,
        "threads": threads,
    }
    if threads_error:
        data["threads_error"] = threads_error
    return data


def _fetch_review_threads(
    owner: str, repo: str, pr_number: int
) -> tuple[list[dict[str, Any]], str | None]:
    """Returns the PR review threads and, on a GitHub failure, no threads and the error."""
    try:
        return list_review_threads(owner, repo, pr_number, user_agent="XMC-PR-Review-Agent"), None
    except (GithubAuthError, GithubAPIError) as e:
        return [], str(e)


def _get_resolved_thread_ids(
    owner: str,
    repo: str,
    pr_number: int,
    comments: list[dict[str, Any]],
    threads: list[dict[str, Any]] | None = None,
) -> set[int]:
    """Retrieve set of resolved review thread comment IDs via GraphQL API or telemetry tag check.

    Uses `threads` when already fetched; otherwise fetches them.
    """
    resolved_ids: set[int] = set()
    if threads is None:
        threads, _ = _fetch_review_threads(owner, repo, pr_number)
    for thread in threads:
        if not thread.get("isResolved"):
            continue
        comment_nodes = (thread.get("comments") or {}).get("nodes") or []
        first_comment = comment_nodes[0] if comment_nodes else None
        if first_comment and first_comment.get("databaseId"):
            resolved_ids.add(int(first_comment["databaseId"]))

    for c in comments:
        cid = c.get("id")
        if cid and bool(c.get("is_resolved") or c.get("resolved")):
            resolved_ids.add(int(cid))

    return resolved_ids


def format_initial_audit_markdown(
    owner: str, repo: str, pr_number: int, data: dict[str, Any]
) -> str:
    """Format the initial review audit checklist markdown artifact."""
    pr = data["pr_info"]
    files = data["files"]
    comments = data["comments"]

    title = pr.get("title", "")
    body = pr.get("body", "") or "No description provided."
    head_sha = (pr.get("head") or {}).get("sha", "unknown")
    base_branch = (pr.get("base") or {}).get("ref", "main")
    head_branch = (pr.get("head") or {}).get("ref", "unknown")
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

    resolved_thread_ids = _get_resolved_thread_ids(
        owner, repo, pr_number, comments, data.get("threads")
    )

    # Summarize existing inline comments to prevent duplication
    if comments:
        open_threads = []
        resolved_threads = []
        for c in comments:
            cid = c.get("id")
            path = c.get("path")
            line = c.get("line") or c.get("original_line")
            user = (c.get("user") or {}).get("login", "unknown")
            is_res = int(cid) in resolved_thread_ids if cid else False
            c_body = (c.get("body", "") or "").replace("\n", " ")
            if len(c_body) > 80:
                c_body = c_body[:77] + "..."
            entry = f"- **Comment ID `{cid}`** | File: `{path}` | Line: `{line}` | Author: `{user}` | Text: *{c_body}*"
            if is_res:
                resolved_threads.append(entry)
            else:
                open_threads.append(entry)

        existing_lines = []
        if open_threads:
            existing_lines.append("**Open Review Threads (Active Context):**")
            existing_lines.extend(open_threads)
        if resolved_threads:
            existing_lines.append(
                "\n**Already Resolved Threads on GitHub UI (CLOSED - Do Not Re-Open):**"
            )
            existing_lines.extend(resolved_threads)
        existing_section = "\n".join(existing_lines)
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

## Core 8-Pillar Baseline Audit Matrix (Deterministic Check)
*Every PR review MUST evaluate all 8 core engineering pillars. Pillars that do not apply to this PR MUST be explicitly marked as `N/A` with a brief justification to prevent stochastic omission:*

- [ ] **Pillar 1: Linear Control Flow & Guard Clauses** (`core_engineering.md#2-kiss`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Evaluate early return guards and zero redundant else blocks.
- [ ] **Pillar 2: Single Responsibility Principle & Target Complexity Limit** (`core_engineering.md#1-srp`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Check function scope and 20-30 line target limits.
- [ ] **Pillar 3: Defensive Input Validation & Boundary Checks** (`core_engineering.md`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Verify nil guards, empty slices, and range upper-bounds.
- [ ] **Pillar 4: Concurrency & `context.Context` Propagation Safety** (`golang.md` / `python.md`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Check context cancellation, mutex boundaries, and race safety.
- [ ] **Pillar 5: Test Double Fidelity & Deterministic Test Execution** (`core_engineering.md#4`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Verify high-fidelity test doubles; prevent silent mock failures.
- [ ] **Pillar 6: Backward Compatibility & Contract Protection** (`core_engineering.md`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Check Protobuf schema, Helm, Spanner DDL, and gRPC stability.
- [ ] **Pillar 7: Dependency Graph & Build System Boundaries** (`core_engineering.md`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Verify Bazel target dependencies (`BUILD.bazel`) and import graphs.
- [ ] **Pillar 8: Documentation & Contract Alignment** (`core_engineering.md#5`)
  *Evaluation*: `[Pending / Pass / Actionable / N/A]` — *Notes*: Ensure README.md, inline comments, and markdown docs stay in sync.

---

## Proposed Inline Review Comments (Confirmation Checklist)
*Populate proposed inline comments below during Phase 3 review synthesis. All proposed items require explicit user confirmation prior to posting:*

| Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- |
| #- | Draft | `pkg/example/example.go` | `10` | `core_engineering.md` | *Sample proposed comment* |
"""


def _format_delta_line(delta: dict[str, Any] | None) -> str:
    """Formats the re-review delta bullet line, or an empty string when absent."""
    if not delta:
        return ""
    verdict = "FULL REVIEW RECOMMENDED" if delta["full_review_recommended"] else "delta review"
    since = delta.get("since_sha") or "none"
    return f"- **Delta Since Last Review** (`{since}`): {verdict} — {delta['reason']}\n"


def format_rereview_audit_markdown(
    owner: str, repo: str, pr_number: int, data: dict[str, Any]
) -> str:
    """Format the follow-up re-review audit report markdown artifact."""
    pr = data["pr_info"]
    comments = data["comments"]
    title = pr.get("title", "")
    head_sha = (pr.get("head") or {}).get("sha", "unknown")

    # Group comment threads
    root_comments = [c for c in comments if not c.get("in_reply_to_id")]
    replies_by_root: dict[int, list[dict[str, Any]]] = collections.defaultdict(list)
    for c in comments:
        reply_to = c.get("in_reply_to_id")
        if reply_to:
            replies_by_root[reply_to].append(c)

    resolved_thread_ids = _get_resolved_thread_ids(
        owner, repo, pr_number, comments, data.get("threads")
    )

    thread_blocks = []
    for root in root_comments:
        cid = root.get("id")
        path = root.get("path")
        line = root.get("line") or root.get("original_line")
        root_author = (root.get("user") or {}).get("login", "unknown")
        root_text = root.get("body", "")
        is_resolved = (int(cid) in resolved_thread_ids if cid else False) or bool(
            root.get("is_resolved") or root.get("resolved")
        )

        thread_replies = replies_by_root.get(cid, [])
        latest_reply = thread_replies[-1] if thread_replies else None
        latest_author = (
            (latest_reply.get("user") or {}).get("login", "none") if latest_reply else "none"
        )
        latest_text = latest_reply.get("body", "") if latest_reply else "None"

        if is_resolved:
            status_line = (
                "- **Audit Verification Status**: `- [x] Already Resolved on GitHub UI (Bucket 1)`"
            )
            action_line = "- **Recommended Action**: `[SKIPPED / ALREADY RESOLVED]`"
        else:
            status_line = "- **Audit Verification Status**: `- [ ] Pending Analysis`"
            action_line = "- **Recommended Action**: `[RESOLVE]` or `[RESPOND]`"

        thread_blocks.append(
            f"""### Thread ID `{cid}` (`{path}`:L{line})
- **Original Reviewer (`{root_author}`)**:
  > {root_text}
- **Latest Reply (`{latest_author}`)**:
  > {latest_text}
{status_line}
{action_line}
- **Proposed Counter-Reply / Rationale**:
  *Leave blank if recommending [RESOLVE] or if already resolved on GitHub, or specify proposed comment below:*

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
{_format_delta_line(data.get("rereview_delta"))}
> [!IMPORTANT]
> **Instructions for Reviewer Iteration**:
> 1. Review each thread verification status below.
> 2. You may edit **Recommended Action** (`[RESOLVE]` or `[RESPOND]`) and **Proposed Counter-Reply / Rationale** inline in this markdown file.
> 3. Once satisfied, confirm to batch-apply resolutions or post inline counter-replies.

---

## Review Thread Re-Evaluation Matrix ({len(root_comments)} threads)

{threads_section}
"""


def build_audit_sidecar(
    owner: str, repo: str, pr_number: int, mode: str, data: dict[str, Any]
) -> dict[str, Any]:
    """Builds the machine-readable JSON sidecar of an audit artifact.

    The sidecar is the source of truth for `review_pr_verify_posted.py`, so item
    statuses never depend on hand-edited markdown. Initial mode starts with no
    items; re-review mode lists every root thread with a null `action` for the
    reviewer to set to `RESOLVE` or `RESPOND`.

    Args:
        owner: Repository owner.
        repo: Repository name.
        pr_number: Pull Request number.
        mode: Audit mode, `initial` or `re-review`.
        data: PR data from `fetch_pr_audit_data`.

    Returns:
        The sidecar document (PR identity and `items`).
    """
    items: list[dict[str, Any]] = []
    if mode == "re-review":
        items = [
            {
                "item_id": str(c.get("id")),
                "type": "re_review_thread",
                "thread_id": c.get("id"),
                "file_path": c.get("path"),
                "line": c.get("line") or c.get("original_line"),
                "action": None,
                "status": "Pending",
            }
            for c in data["comments"]
            if c.get("id") and not c.get("in_reply_to_id")
        ]
    return {
        "owner": owner,
        "repo": repo,
        "pr_number": pr_number,
        "mode": mode,
        "head_sha": ((data["pr_info"] or {}).get("head") or {}).get("sha"),
        "items": items,
    }


def _prepare_workspace(args: argparse.Namespace, data: dict[str, Any]) -> dict[str, Any]:
    """Measures the re-review delta and writes the PR-head review inputs when requested."""
    if args.mode == "re-review":
        head_sha = ((data["pr_info"] or {}).get("head") or {}).get("sha") or ""
        data["rereview_delta"] = measure_rereview_delta(
            args.owner,
            args.repo,
            args.pr_number,
            head_sha,
            since_sha=args.since_sha,
            threshold=args.full_review_threshold,
            comments=data["comments"],
        )
    if not args.out_dir:
        return {}
    return prepare_review_workspace(
        args.owner,
        args.repo,
        args.pr_number,
        data,
        Path(args.out_dir),
        checkout=args.checkout,
        remote=args.remote,
        rereview_delta=data.get("rereview_delta"),
    )


def main(args: argparse.Namespace) -> dict[str, Any]:
    """Execute PR audit checklist generation."""
    if args.checkout and not args.out_dir:
        raise HandledError("--checkout requires --out-dir.", "Pass --out-dir <session_dir>.")
    data = fetch_pr_audit_data(args.owner, args.repo, args.pr_number)
    workspace = _prepare_workspace(args, data)

    if args.mode == "re-review":
        default_filename = "pr_rereview_audit_report.md"
        content = format_rereview_audit_markdown(args.owner, args.repo, args.pr_number, data)
    else:
        default_filename = "pr_review_audit_checklist.md"
        content = format_initial_audit_markdown(args.owner, args.repo, args.pr_number, data)

    out_path = resolve_output_path(args.output, args.conv_id, default_filename, args.out_dir)
    out_path.write_text(content, encoding="utf-8")
    sidecar_path = out_path.with_suffix(".json")
    sidecar = build_audit_sidecar(args.owner, args.repo, args.pr_number, args.mode, data)
    sidecar_path.write_text(json.dumps(sidecar, indent=2) + "\n", encoding="utf-8")

    optional: dict[str, Any] = {
        k: data[k] for k in ("rereview_delta", "threads_error") if k in data
    }
    return {
        "status": "success",
        "mode": args.mode,
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "output_file": str(out_path.resolve()),
        "sidecar_file": str(sidecar_path.resolve()),
        "file_count": len(data["files"]),
        "existing_comment_count": len(data["comments"]),
        **optional,
        **workspace,
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
    parser.add_argument(
        "--out-dir",
        help="Session scratch directory outside the repository: fetches the PR head, writes "
        "pr.diff, pr_meta.json, review_threads.json, and (without -o) the audit markdown.",
    )
    parser.add_argument(
        "--checkout",
        action="store_true",
        help="Check out the PR head detached (requires --out-dir); with local changes to "
        "tracked files, skips and returns checkout_command instead.",
    )
    parser.add_argument("--remote", default="origin", help="Git remote hosting the PR.")
    parser.add_argument(
        "--since-sha",
        help="Re-review: last reviewed commit (default: your latest review's commit).",
    )
    parser.add_argument(
        "--full-review-threshold",
        type=int,
        default=DEFAULT_FULL_REVIEW_THRESHOLD,
        help="Re-review: changed lines since the last review above which a full review "
        "is recommended.",
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), main)
