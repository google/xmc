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

"""Streamlined helper tool to dispatch GitHub Actions release workflows, poll, and watch outcomes."""

import argparse
import json
import shutil
import subprocess
import time
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_tool

EXIT_SUCCESS = 0
EXIT_FAILURE = 1
EXIT_CANCELLED = 2
EXIT_SHA_MISMATCH = 3


def _run_gh(args: list[str], repo_dir: Path | None = None) -> subprocess.CompletedProcess[str]:
    if not shutil.which("gh"):
        raise HandledError(
            "GitHub CLI ('gh') is not installed or not found in PATH.",
            "Install gh CLI and authenticate with 'gh auth login'.",
        )
    return subprocess.run(
        ["gh", *args],
        capture_output=True,
        text=True,
        check=False,
        cwd=str(repo_dir) if repo_dir else None,
    )


def dispatch_and_watch(args: argparse.Namespace) -> dict[str, Any]:
    """Dispatches a workflow, verifies target SHA, watches run, and returns structured summary."""
    workflow, ref = args.workflow, getattr(args, "ref", "main") or "main"
    target_sha = (getattr(args, "target_sha", "") or "").strip()
    repo_dir = Path(args.repo_dir) if getattr(args, "repo_dir", None) else None

    # 1. Check for existing successful run if --find-existing
    if args.find_existing:
        cmd = [
            "run",
            "list",
            f"--workflow={workflow}",
            f"--branch={ref}",
            "--status=success",
            "--limit=5",
            "--json=databaseId,headSha,url,conclusion",
        ]
        res = _run_gh(cmd, repo_dir)
        if res.returncode == 0 and res.stdout.strip():
            for run in json.loads(res.stdout):
                if not target_sha or run.get("headSha", "").startswith(target_sha):
                    return {
                        "run_id": run["databaseId"],
                        "url": run.get("url", ""),
                        "head_sha": run.get("headSha", ""),
                        "conclusion": "success",
                        "failed_jobs": [],
                        "_exit_code": EXIT_SUCCESS,
                    }

    # 2. Dispatch workflow run
    res = _run_gh(["workflow", "run", workflow, "--ref", ref], repo_dir)
    if res.returncode != 0:
        raise HandledError(
            f"Failed to dispatch workflow '{workflow}': {res.stderr.strip()}",
            "Verify workflow name and GitHub permissions.",
        )

    # 3. Poll for registered workflow run
    list_cmd = [
        "run",
        "list",
        f"--workflow={workflow}",
        f"--branch={ref}",
        "--event=workflow_dispatch",
        "--limit=5",
        "--json=databaseId,headSha,url,createdAt",
    ]
    run_data: dict[str, Any] | None = None
    for attempt in range(args.poll_attempts):
        if attempt > 0:
            time.sleep(args.poll_interval)
        res = _run_gh(list_cmd, repo_dir)
        if res.returncode == 0 and res.stdout.strip():
            runs = json.loads(res.stdout)
            if runs:
                run_data = runs[0]
                break

    if not run_data:
        raise HandledError(
            f"Timed out waiting for '{workflow}' run to register on ref '{ref}'.",
            "Check GitHub Actions status.",
        )

    run_id = int(run_data["databaseId"])
    run_sha = run_data.get("headSha", "")
    run_url = run_data.get("url", f"https://github.com/google/xmc/actions/runs/{run_id}")

    # 4. Strict commit SHA verification
    if target_sha and not run_sha.startswith(target_sha):
        return {
            "run_id": run_id,
            "url": run_url,
            "head_sha": run_sha,
            "conclusion": "sha_mismatch",
            "failed_jobs": [],
            "_exit_code": EXIT_SHA_MISMATCH,
        }

    # 5. Watch to completion via `gh run watch`
    res = _run_gh(["run", "watch", str(run_id), "--exit-status"], repo_dir)
    view_res = _run_gh(["run", "view", str(run_id), "--json=conclusion,jobs"], repo_dir)
    view_data = (
        json.loads(view_res.stdout) if view_res.returncode == 0 and view_res.stdout.strip() else {}
    )
    conclusion = view_data.get("conclusion") or ("success" if res.returncode == 0 else "failure")

    failed_jobs = [
        {"name": j.get("name", ""), "conclusion": j.get("conclusion", "")}
        for j in view_data.get("jobs", [])
        if j.get("conclusion") == "failure"
    ]

    exit_code = (
        EXIT_SUCCESS
        if conclusion == "success"
        else (EXIT_CANCELLED if conclusion == "cancelled" else EXIT_FAILURE)
    )
    return {
        "run_id": run_id,
        "url": run_url,
        "head_sha": run_sha,
        "conclusion": conclusion,
        "failed_jobs": failed_jobs,
        "_exit_code": exit_code,
    }


def create_parser() -> argparse.ArgumentParser:
    """Constructs CLI argument parser."""
    p = argparse.ArgumentParser(
        description="Dispatches a GitHub Actions workflow and watches outcome."
    )
    p.add_argument("--workflow", "-w", required=True, help="Workflow filename.")
    p.add_argument("--target-sha", "--sha", "-s", default="", help="Expected commit SHA.")
    p.add_argument("--ref", "-r", default="main", help="Git branch or ref.")
    p.add_argument(
        "--find-existing",
        action="store_true",
        help="Reuse existing successful run.",
    )
    p.add_argument("--state-file", help="Path to JSON state file (ignored).")
    p.add_argument("--poll-interval", type=float, default=5.0, help="Poll interval.")
    p.add_argument("--poll-attempts", type=int, default=12, help="Max poll attempts.")
    p.add_argument("--repo-dir", default=".", help="Repository root.")
    return p


def main() -> None:
    run_tool(create_parser(), dispatch_and_watch)


if __name__ == "__main__":
    main()
