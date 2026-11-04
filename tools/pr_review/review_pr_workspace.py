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

"""Pins a PR review to the pull request head and writes its review inputs.

Reviewers that read files from a stale checkout review the wrong code. This
module fetches `pull/<n>/head`, optionally checks it out detached (only when the
worktree has no tracked changes), and writes `pr.diff`, `pr_meta.json`, and
`review_threads.json` into a caller-chosen directory so reviewers without a
shell can read them. In re-review mode it also measures how much changed since
the last review by the authenticated user.
"""

import json
import sys
from collections.abc import Iterable
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_command
from tools.pr_review.github_utils import (
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    get_authenticated_login,
    list_all_pages,
)

DEFAULT_FULL_REVIEW_THRESHOLD = 200
_USER_AGENT = "XMC-PR-Review-Agent"
_FILE_FIELDS = ("filename", "status", "additions", "deletions")
_GIT_INSTRUCTION = (
    "Run from a clone of the PR repository whose --remote points at it, "
    "and check network access and git credentials."
)


def _git(args: list[str]) -> str:
    """Runs a git command, keeping stdout free for the tool's JSON output."""
    proc = run_command(
        ["git", *args],
        capture_output=True,
        stream=sys.stderr,
        handled_instruction=_GIT_INSTRUCTION,
    )
    return proc.stdout


def _has_commit(sha: str) -> bool:
    """Returns whether `sha` names a commit in the local repository."""
    proc = run_command(
        ["git", "cat-file", "-e", f"{sha}^{{commit}}"],
        check=False,
        capture_output=True,
        log_cmd=False,
        handled_instruction=_GIT_INSTRUCTION,
    )
    return proc.returncode == 0


def fetch_pr_head(remote: str, pr_number: int, base_sha: str, head_sha: str) -> None:
    """Fetches the PR head, plus the base commit when it is not already local.

    A missing base is fetched by SHA, not branch name, because stacked PRs keep
    pointing at a base branch that is deleted once it merges.

    Raises:
        HandledError: If the fetch fails, or `head_sha` is still missing because
            the PR head moved after it was read from the API.
    """
    base = [] if _has_commit(base_sha) else [base_sha]
    _git(["fetch", "--no-tags", remote, f"pull/{pr_number}/head", *base])
    if not _has_commit(head_sha):
        raise HandledError(
            f"PR head {head_sha} was not fetched; the PR changed during the run.",
            "Re-run the tool to pick up the new head.",
        )


def checkout_pr_head(head_sha: str) -> dict[str, Any]:
    """Checks out the PR head detached unless tracked files have local changes.

    Local changes are never discarded: with a dirty worktree, or when git refuses
    the checkout (e.g. an untracked file at a path the PR adds), the checkout is
    skipped and the exact command is returned for the caller to run later.

    Args:
        head_sha: PR head commit SHA, already fetched.

    Returns:
        `checked_out` and, when skipped, `checkout_command` and `reason`.
    """
    command = ["git", "checkout", "--detach", head_sha]
    skipped = {"checked_out": False, "checkout_command": " ".join(command)}
    if _git(["status", "--porcelain", "--untracked-files=no"]).strip():
        return skipped | {"reason": "Worktree has uncommitted changes to tracked files."}
    try:
        _git(command[1:])
    except HandledError as e:
        return skipped | {"reason": f"git refused the checkout: {e.message}"}
    return {"checked_out": True}


def build_pr_meta(owner: str, repo: str, pr_number: int, data: dict[str, Any]) -> dict[str, Any]:
    """Builds `pr_meta.json`: PR identity, base and head refs and SHAs, and files."""
    pr = data["pr_info"]
    base = pr.get("base") or {}
    head = pr.get("head") or {}
    return {
        "owner": owner,
        "repo": repo,
        "pr_number": pr_number,
        "title": pr.get("title"),
        "author": (pr.get("user") or {}).get("login"),
        "base_ref": base.get("ref"),
        "base_sha": base.get("sha"),
        "head_ref": head.get("ref"),
        "head_sha": head.get("sha"),
        "files": [{k: f.get(k) for k in _FILE_FIELDS} for f in data["files"]],
    }


def _reply_only_review_ids(comments: Iterable[dict[str, Any]]) -> set[int]:
    """Returns ids of reviews GitHub created only to wrap standalone thread replies.

    Each such wrapper is pinned to the PR head at reply time, not to reviewed code.
    """
    reply = {c.get("pull_request_review_id") for c in comments if c.get("in_reply_to_id")}
    root = {c.get("pull_request_review_id") for c in comments if not c.get("in_reply_to_id")}
    return {i for i in reply - root if i is not None}


def find_last_reviewed_sha(
    owner: str, repo: str, pr_number: int, comments: Iterable[dict[str, Any]] = ()
) -> str | None:
    """Returns the commit of the authenticated user's latest real review.

    Pending drafts and reply-only wrapper reviews (empty `COMMENTED` reviews whose
    only comments are replies, per `comments`) are skipped.
    """
    login = get_authenticated_login(_USER_AGENT)
    reviews = list_all_pages(f"/repos/{owner}/{repo}/pulls/{pr_number}/reviews", _USER_AGENT)
    reply_only = _reply_only_review_ids(comments)
    shas = [
        r["commit_id"]
        for r in reviews
        if (r.get("user") or {}).get("login") == login
        and r.get("commit_id")
        and r.get("state") != "PENDING"
        and not (r.get("id") in reply_only and r.get("state") == "COMMENTED" and not r.get("body"))
    ]
    return shas[-1] if shas else None


def _unknown_delta(since_sha: str | None, reason: str) -> dict[str, Any]:
    """Returns a delta of unknown size, which always recommends a full review."""
    return {
        "since_sha": since_sha,
        "changed_lines": None,
        "changed_files": [],
        "full_review_recommended": True,
        "reason": reason,
    }


def _compare_delta(
    owner: str, repo: str, since_sha: str, head_sha: str, threshold: int
) -> dict[str, Any]:
    """Counts lines changed in GitHub's `since...head` comparison against `threshold`."""
    try:
        compare = call_github_api(
            f"/repos/{owner}/{repo}/compare/{since_sha}...{head_sha}", user_agent=_USER_AGENT
        )
    except (GithubAuthError, GithubAPIError) as e:
        return _unknown_delta(since_sha, f"Cannot compare {since_sha} with the head: {e}")
    files = compare.get("files") or []
    changed = sum(f.get("additions", 0) + f.get("deletions", 0) for f in files)
    recommended = changed > threshold
    return {
        "since_sha": since_sha,
        "changed_lines": changed,
        "changed_files": [f.get("filename") for f in files],
        "full_review_recommended": recommended,
        "reason": f"{changed} changed lines {'>' if recommended else '<='} threshold {threshold}.",
    }


def _write_json(path: Path, content: Any) -> None:
    path.write_text(json.dumps(content, indent=2) + "\n", encoding="utf-8")


def measure_rereview_delta(
    owner: str,
    repo: str,
    pr_number: int,
    head_sha: str,
    since_sha: str | None = None,
    threshold: int = DEFAULT_FULL_REVIEW_THRESHOLD,
    comments: Iterable[dict[str, Any]] = (),
) -> dict[str, Any]:
    """Counts lines changed since `since_sha`, defaulting to the user's last review.

    The count comes from GitHub's `since...head` comparison, so commits merged in
    from the base branch or a rebase inflate it; the error only over-recommends a
    full review. With no earlier review or no comparison, the size is unknown and
    a full review is recommended.

    Args:
        owner: Repository owner.
        repo: Repository name.
        pr_number: Pull Request number.
        head_sha: Current PR head commit.
        since_sha: Explicit last reviewed commit; looked up when None.
        threshold: Changed-line count above which a full review is recommended.
        comments: PR review comments, used to skip reply-only wrapper reviews.

    Returns:
        `since_sha`, `changed_lines` (None when unknown), `changed_files`,
        `full_review_recommended`, and `reason`.
    """
    if not since_sha:
        try:
            since_sha = find_last_reviewed_sha(owner, repo, pr_number, comments)
        except (GithubAuthError, GithubAPIError) as e:
            return _unknown_delta(None, f"Cannot look up the last review: {e}")
    if not since_sha:
        return _unknown_delta(None, "No earlier review found.")
    return _compare_delta(owner, repo, since_sha, head_sha, threshold)


def prepare_review_workspace(
    owner: str,
    repo: str,
    pr_number: int,
    data: dict[str, Any],
    out_dir: Path,
    *,
    checkout: bool = False,
    remote: str = "origin",
    rereview_delta: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Fetches the PR head and writes the review inputs into `out_dir`.

    Args:
        owner: Repository owner.
        repo: Repository name.
        pr_number: Pull Request number.
        data: PR data from `fetch_pr_audit_data` (`pr_info`, `files`, `threads`).
        out_dir: Directory receiving `pr.diff`, `pr_meta.json`, `review_threads.json`.
        checkout: Whether to check out the PR head detached in the current worktree.
        remote: Git remote that hosts the PR.
        rereview_delta: Result of `measure_rereview_delta`, recorded in `pr_meta.json`.

    Returns:
        Paths of the written files plus the checkout result.

    Raises:
        HandledError: If a git command fails.
    """
    meta = build_pr_meta(owner, repo, pr_number, data)
    if not (meta["base_sha"] and meta["head_sha"]):
        raise HandledError(
            f"PR {owner}/{repo}#{pr_number} response has no base or head SHA.",
            "Re-run the tool; if it persists, check the PR exists and is not deleted.",
        )
    fetch_pr_head(remote, pr_number, meta["base_sha"], meta["head_sha"])
    out_dir.mkdir(parents=True, exist_ok=True)
    diff_path = out_dir / "pr.diff"
    diff = _git(["diff", f"{meta['base_sha']}...{meta['head_sha']}"])
    diff_path.write_text(diff, encoding="utf-8")
    if rereview_delta is not None:
        meta["rereview_delta"] = rereview_delta
    if "threads_error" in data:
        meta["threads_error"] = data["threads_error"]
    meta_path = out_dir / "pr_meta.json"
    _write_json(meta_path, meta)
    threads_path = out_dir / "review_threads.json"
    _write_json(threads_path, data.get("threads") or [])
    result = {"checked_out": False}
    if checkout:
        result = checkout_pr_head(meta["head_sha"])
    return result | {
        "diff_file": str(diff_path.resolve()),
        "meta_file": str(meta_path.resolve()),
        "threads_file": str(threads_path.resolve()),
    }
