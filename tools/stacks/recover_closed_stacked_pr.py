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

"""Recovers a stacked PR closed by GitHub when an intermediate base branch was deleted.

Tested with GitHub CLI (`gh`) v2.46.0+ and `gh-stack` extension v0.1.0+.

Example:
    $ ./tools/stacks/recover_closed_stacked_pr.py 753 --dry-run
    $ ./tools/stacks/recover_closed_stacked_pr.py 753 --target-base main
"""

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_REPO_ROOT = str(Path(__file__).resolve().parent.parent.parent)
if _REPO_ROOT not in sys.path:
    sys.path.insert(0, _REPO_ROOT)

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.pr_review.github_utils import (  # noqa: E402
    GithubAPIError,
    GithubAuthError,
    call_github_api,
)


@dataclass(frozen=True)
class PullRequestIdentifier:
    """Holds parsed components of a GitHub pull request identifier."""

    owner: str
    repo: str
    pull_number: int

    def __getitem__(self, item: str) -> Any:
        return getattr(self, item)


def parse_pr_identifier(pr_arg: str) -> PullRequestIdentifier:
    """Parses a PR number or GitHub PR URL into owner, repo, and PR number."""
    if pr_arg.isdigit():
        return PullRequestIdentifier(owner="google", repo="xmc", pull_number=int(pr_arg))
    if m := re.search(r"github\.com/([^/]+)/([^/]+)/pull/(\d+)", pr_arg):
        return PullRequestIdentifier(m.group(1), m.group(2), int(m.group(3)))
    msg = "Provide a numeric PR ID (e.g. '753') or full URL (e.g. 'https://github.com/.../753')."
    raise HandledError(f"Invalid PR identifier: '{pr_arg}'", msg)


def _run_subprocess_command(
    cmd: list[str], check: bool = True, capture_output: bool = True
) -> subprocess.CompletedProcess[str]:
    """Runs a subprocess command with string output decoding."""
    try:
        return subprocess.run(cmd, check=check, capture_output=capture_output, text=True)
    except FileNotFoundError as e:
        msg = f"Please ensure '{cmd[0]}' is installed and available in your PATH."
        raise HandledError(f"Command '{cmd[0]}' not found.", msg) from e


def _is_valid_git_commit(commit_sha: str) -> bool:
    """Checks whether the given commit SHA exists in local git history."""
    if not commit_sha or not re.fullmatch(r"[0-9a-fA-F]{7,40}", commit_sha):
        return False
    cmd = ["git", "rev-parse", "--verify", "--quiet", f"{commit_sha}^{{commit}}"]
    return _run_subprocess_command(cmd, check=False).returncode == 0


def _resolve_merge_base(remote: str, target_base: str, head_ref: str) -> str | None:
    """Finds merge-base commit SHA between head_ref and target_base."""
    for target in [f"{remote}/{target_base}", target_base]:
        proc = _run_subprocess_command(["git", "merge-base", target, head_ref], check=False)
        if proc.returncode == 0 and proc.stdout.strip():
            return proc.stdout.strip()
    return None


def _resolve_target_base_sha(remote: str, target_base: str) -> str | None:
    """Resolves commit SHA of the target base branch."""
    for ref in [f"{remote}/{target_base}^{{commit}}", f"{target_base}^{{commit}}"]:
        p = _run_subprocess_command(["git", "rev-parse", "--verify", "--quiet", ref], check=False)
        if p.returncode == 0 and p.stdout.strip():
            return p.stdout.strip()
    return None


def resolve_base_commit_sha(pr_data: dict[str, Any], target_base: str, remote: str) -> str:
    """Resolves a commit SHA to recreate the deleted base branch."""
    base = pr_data.get("base", {})
    if (base_sha := base.get("sha", "")) and _is_valid_git_commit(base_sha):
        return base_sha
    head_ref = pr_data.get("head", {}).get("ref", "")
    if sha := _resolve_merge_base(remote, target_base, head_ref) or _resolve_target_base_sha(
        remote, target_base
    ):
        return sha
    base_ref = base.get("ref", "")
    raise HandledError(
        f"Failed to resolve commit SHA for deleted base branch '{base_ref}'.",
        f"Run 'git fetch {remote}' to make base commits available locally.",
    )


def fetch_pr_details(pr_id: PullRequestIdentifier) -> dict[str, Any]:
    """Fetches Pull Request metadata from GitHub API."""
    url = f"/repos/{pr_id.owner}/{pr_id.repo}/pulls/{pr_id.pull_number}"
    try:
        resp = call_github_api(url, method="GET", user_agent="XMC-Stack-Recovery-Agent")
    except GithubAuthError as e:
        msg = "Please authenticate using 'gh auth login' or set GITHUB_TOKEN."
        raise HandledError(str(e), msg) from e
    except GithubAPIError as e:
        msg = "Ensure the pull request exists and your credentials have read access."
        raise HandledError(str(e), msg) from e
    if not isinstance(resp, dict):
        msg = "Ensure GitHub API returns a valid JSON object."
        raise HandledError(f"Unexpected GitHub API response for PR #{pr_id.pull_number}.", msg)
    return resp


def discover_upstream_prs(
    owner: str, repo: str, start_head_ref: str, max_depth: int = 50
) -> list[dict[str, Any]]:
    """Recursively discovers upstream stacked pull requests."""
    chain, visited_prs, current_base = [], set(), start_head_ref
    for _ in range(max_depth):
        url = f"/repos/{owner}/{repo}/pulls?state=open&base={current_base}&per_page=100"
        try:
            results = call_github_api(url, method="GET", user_agent="XMC-Stack-Recovery-Agent")
        except (GithubAPIError, GithubAuthError) as e:
            msg = "Verify GitHub API access and network connectivity."
            raise HandledError(
                f"Failed to query upstream PRs for base '{current_base}': {e}", msg
            ) from e
        if not isinstance(results, list) or not results:
            break
        child_pr = results[0]
        if not isinstance(child_pr, dict):
            break
        if not (child_num := child_pr.get("number")) or child_num in visited_prs:
            break
        visited_prs.add(child_num)
        chain.append(child_pr)
        if not (current_base := child_pr.get("head", {}).get("ref", "")):
            break
    return chain


def unstack_stack(unstack_local_only: bool = False, dry_run: bool = False) -> None:
    """Unstacks current stack locally or remotely with fallback handling."""
    if not dry_run:
        cmd = ["gh", "stack", "unstack"] + (["--local"] if unstack_local_only else [])
        try:
            _run_subprocess_command(cmd)
        except subprocess.CalledProcessError:
            if not unstack_local_only:
                _run_subprocess_command(["gh", "stack", "unstack", "--local"])


def remote_branch_exists(remote: str, branch_name: str) -> bool:
    """Checks whether a branch exists on the remote repository."""
    cmd = ["git", "ls-remote", "--heads", remote, branch_name]
    proc = _run_subprocess_command(cmd, check=False)
    return proc.returncode == 0 and f"refs/heads/{branch_name}" in proc.stdout


def recreate_remote_branch(remote: str, sha: str, branch: str, dry_run: bool = False) -> None:
    """Temporarily recreates deleted base branch on remote repository."""
    if not dry_run:
        _run_subprocess_command(["git", "push", remote, f"{sha}:refs/heads/{branch}"])


def _update_pr(owner: str, repo: str, pr_num: int, payload: dict[str, str], dry_run: bool) -> None:
    if not dry_run:
        call_github_api(
            f"/repos/{owner}/{repo}/pulls/{pr_num}",
            method="PATCH",
            payload=payload,
            user_agent="XMC-Stack-Recovery-Agent",
        )


def reopen_pr(owner: str, repo: str, pr_number: int, dry_run: bool = False) -> None:
    """Reopens a closed pull request via GitHub API."""
    try:
        _update_pr(owner, repo, pr_number, {"state": "open"}, dry_run)
    except (GithubAPIError, GithubAuthError) as e:
        msg = "Ensure the temporary base branch was pushed and you have write permissions."
        raise HandledError(f"Failed to reopen PR #{pr_number}: {e}", msg) from e


def retarget_pr_base(
    owner: str, repo: str, pr_num: int, target: str, dry_run: bool = False
) -> None:
    """Retargets pull request base branch to target base branch."""
    try:
        _update_pr(owner, repo, pr_num, {"base": target}, dry_run)
    except (GithubAPIError, GithubAuthError) as e:
        msg = "Ensure the PR is open and target base branch exists."
        raise HandledError(f"Failed to retarget PR #{pr_num} base to '{target}': {e}", msg) from e


def delete_remote_branch(remote: str, branch: str, dry_run: bool = False) -> None:
    """Deletes temporary base branch from remote repository."""
    if not dry_run:
        _run_subprocess_command(["git", "push", remote, "--delete", branch])


def rebuild_and_sync_stack(target_base: str, branches: list[str], dry_run: bool = False) -> None:
    """Re-initializes local stack and submits stack to GitHub."""
    if not dry_run:
        _run_subprocess_command(["gh", "stack", "init", "--base", target_base] + branches)
        try:
            _run_subprocess_command(["gh", "stack", "submit", "--auto"])
        except subprocess.CalledProcessError:
            _run_subprocess_command(["gh", "stack", "sync"])


def recover_stacked_pr(args: argparse.Namespace) -> dict[str, Any]:
    """Recovers a closed stacked PR and restacks the remaining pull requests."""
    parsed_pr = parse_pr_identifier(args.pr)
    owner, repo, pr_num = parsed_pr.owner, parsed_pr.repo, parsed_pr.pull_number
    target_base, remote, dry_run = args.target_base, args.remote, args.dry_run

    pr_data = fetch_pr_details(parsed_pr)
    if pr_data.get("state") == "open":
        raise HandledError(
            f"Pull Request #{pr_num} is already open.", "Only closed PRs can be recovered."
        )
    if pr_data.get("merged") or pr_data.get("merged_at"):
        raise HandledError(
            f"Pull Request #{pr_num} is already merged.", "Merged PRs cannot be recovered."
        )

    del_base = pr_data.get("base", {}).get("ref", "")
    rec_head = pr_data.get("head", {}).get("ref", "")
    if not del_base or not rec_head:
        msg = "Ensure valid branch references on GitHub."
        raise HandledError(f"Pull Request #{pr_num} metadata missing head or base ref.", msg)

    sha = resolve_base_commit_sha(pr_data, target_base, remote)
    deleted = not remote_branch_exists(remote, del_base)

    unstack_stack(unstack_local_only=args.unstack_local_only, dry_run=dry_run)
    if deleted:
        recreate_remote_branch(remote, sha, del_base, dry_run=dry_run)
    try:
        reopen_pr(owner, repo, pr_num, dry_run=dry_run)
        retarget_pr_base(owner, repo, pr_num, target_base, dry_run=dry_run)
    finally:
        if deleted:
            delete_remote_branch(remote, del_base, dry_run=dry_run)

    upstream = discover_upstream_prs(owner, repo, rec_head)
    stack = [rec_head] + [
        p.get("head", {}).get("ref", "") for p in upstream if p.get("head", {}).get("ref")
    ]
    rebuild_and_sync_stack(target_base, stack, dry_run=dry_run)

    return {
        "status": "dry_run" if dry_run else "success",
        "dry_run": dry_run,
        "pull_number": pr_num,
        "target_base": target_base,
        "deleted_base_branch": del_base,
        "remote_branch_was_recreated": deleted,
        "recreated_commit_sha": sha,
        "recovered_head_branch": rec_head,
        "upstream_pr_numbers": [p["number"] for p in upstream if "number" in p],
        "stack_branches": stack,
    }


def create_argument_parser() -> argparse.ArgumentParser:
    """Creates argument parser for recover_closed_stacked_pr."""
    parser = argparse.ArgumentParser(description="Recover closed stacked PR.")
    parser.add_argument("pr", help="Pull Request number or GitHub PR URL.")
    parser.add_argument("--target-base", default="main", help="Target base branch.")
    parser.add_argument("--remote", default="origin", help="Git remote name.")
    parser.add_argument("--dry-run", action="store_true", help="Dry run mode.")
    parser.add_argument("--unstack-local-only", action="store_true", help="Unstack locally only.")
    return parser


def main() -> None:
    """Main entrypoint for recover_closed_stacked_pr tool."""
    parser = create_argument_parser()
    run_tool(parser, recover_stacked_pr)


if __name__ == "__main__":
    main()
