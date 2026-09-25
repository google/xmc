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

"""Helper tool to dispatch GitHub Actions release workflows, poll, and watch outcomes."""

import argparse
import json
import shutil
import subprocess
import time
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_tool
from tools.release.git_ops import resolve_repo_owner_and_name, resolve_target_commit
from tools.release.models import DispatchWatchResult, FailedJobSummary

# Exit codes per contract
EXIT_SUCCESS = 0
EXIT_FAILURE = 1
EXIT_CANCELLED = 2
EXIT_SHA_MISMATCH = 3


@dataclass
class WorkflowRun:
    """Represents a discovered GitHub Actions workflow run."""

    run_id: int
    url: str
    head_sha: str
    event: str
    status: str
    created_at: datetime


@dataclass
class RunOutcome:
    """Represents the final execution outcome of a watched workflow run."""

    run_id: int
    url: str
    head_sha: str
    event: str
    conclusion: str
    superseded_by: int | None
    failed_jobs: list[FailedJobSummary]
    page_url: str | None


def _run_gh_cmd(
    args: list[str],
    repo_dir: Path | None = None,
    err_prefix: str = "GitHub CLI command failed",
    instruction: str = "Ensure gh is authenticated and repository remote is configured.",
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    """Executes a gh CLI command and returns the completed process."""
    if not shutil.which("gh"):
        raise HandledError(
            "GitHub CLI ('gh') is not installed or not found in system PATH.",
            "Please install gh CLI and run 'gh auth login' before running release tooling.",
            exit_code=EXIT_FAILURE,
        )

    cmd = ["gh", *args]
    cwd = str(repo_dir) if repo_dir else None
    try:
        res = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            check=False,
            cwd=cwd,
        )
    except OSError as e:
        raise HandledError(
            f"{err_prefix}: {e}",
            instruction,
            exit_code=EXIT_FAILURE,
        ) from e

    if check and res.returncode != 0:
        err_msg = res.stderr.strip() if res.stderr else f"Exit code {res.returncode}"
        raise HandledError(
            f"{err_prefix}: {err_msg}",
            instruction,
            exit_code=EXIT_FAILURE,
        )
    return res


def parse_iso8601_utc(dt_str: str) -> datetime:
    """Parses an ISO 8601 timestamp string into a timezone-aware UTC datetime."""
    clean_str = dt_str.replace("Z", "+00:00")
    dt = datetime.fromisoformat(clean_str)
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def resolve_actor(repo_dir: Path | None = None) -> str:
    """Resolves the current authenticated GitHub user login."""
    try:
        res = _run_gh_cmd(["api", "user", "-q", ".login"], repo_dir=repo_dir, check=False)
        if res.returncode == 0 and res.stdout.strip():
            return res.stdout.strip()
    except Exception:
        pass
    return ""


def dispatch_workflow(
    workflow: str,
    ref: str = "main",
    repo_dir: Path | None = None,
) -> datetime:
    """Dispatches a workflow via gh and returns the pre-dispatch UTC timestamp."""
    dispatch_time = datetime.now(timezone.utc)
    _run_gh_cmd(
        ["workflow", "run", workflow, "--ref", ref],
        repo_dir=repo_dir,
        err_prefix=f"Failed to dispatch workflow '{workflow}' on ref '{ref}'",
        instruction="Verify that the workflow file exists and workflow_dispatch is enabled.",
    )
    return dispatch_time


def find_existing_run(
    workflow: str,
    target_sha: str,
    ref: str = "main",
    repo_dir: Path | None = None,
) -> WorkflowRun | None:
    """Checks for an existing completed, successful workflow run on the target SHA."""
    cmd = [
        "run",
        "list",
        f"--workflow={workflow}",
        f"--branch={ref}",
        "--status=completed",
        "--limit=10",
        "--json=databaseId,headSha,event,status,conclusion,createdAt,url",
    ]
    if len(target_sha) == 40:
        cmd.append(f"--commit={target_sha}")
    res = _run_gh_cmd(
        cmd,
        repo_dir=repo_dir,
        err_prefix=f"Failed to query existing runs for workflow '{workflow}'",
        instruction="Ensure you have network access and valid GitHub credentials.",
    )
    try:
        runs_data = json.loads(res.stdout)
    except json.JSONDecodeError as e:
        raise HandledError(
            f"Failed to parse gh run list output as JSON: {e}",
            "Check gh output or try again.",
            exit_code=EXIT_FAILURE,
        ) from e

    for run in runs_data:
        head_sha = run.get("headSha", "")
        sha_matches = (
            head_sha == target_sha
            or head_sha.startswith(target_sha)
            or target_sha.startswith(head_sha)
        )
        if sha_matches and run.get("status") == "completed" and run.get("conclusion") == "success":
            return WorkflowRun(
                run_id=int(run["databaseId"]),
                url=run.get("url", ""),
                head_sha=head_sha,
                event=run.get("event", ""),
                status=run.get("status", ""),
                created_at=parse_iso8601_utc(run.get("createdAt", "")),
            )
    return None


def find_dispatched_run(
    workflow: str,
    target_sha: str,
    since: datetime,
    actor: str = "",
    ref: str = "main",
    max_attempts: int = 12,
    poll_interval_s: float = 5.0,
    repo_dir: Path | None = None,
) -> WorkflowRun:
    """Polls for the newly dispatched workflow run and validates target commit SHA."""
    skew_buffer = since - timedelta(seconds=5)

    cmd = [
        "run",
        "list",
        f"--workflow={workflow}",
        f"--branch={ref}",
        "--event=workflow_dispatch",
        "--limit=10",
        "--json=databaseId,headSha,event,status,conclusion,createdAt,url",
    ]
    if actor:
        cmd.append(f"--user={actor}")

    for attempt in range(max_attempts):
        if attempt > 0:
            time.sleep(poll_interval_s)

        res = _run_gh_cmd(
            cmd,
            repo_dir=repo_dir,
            err_prefix=f"Failed to poll runs for workflow '{workflow}'",
            instruction="Ensure you have network access and valid GitHub credentials.",
            check=False,
        )
        if res.returncode != 0:
            continue

        try:
            runs_data = json.loads(res.stdout)
        except json.JSONDecodeError:
            continue

        matching_runs: list[dict[str, Any]] = []
        for run in runs_data:
            if run.get("event") != "workflow_dispatch":
                continue
            created_at_raw = run.get("createdAt")
            if not created_at_raw:
                continue
            try:
                run_dt = parse_iso8601_utc(created_at_raw)
            except Exception:
                continue
            if run_dt >= skew_buffer:
                matching_runs.append(run)

        if matching_runs:
            # Sort newest first
            matching_runs.sort(
                key=lambda r: parse_iso8601_utc(r["createdAt"]),
                reverse=True,
            )

            # 1. Search for a run that matches target commit SHA
            for run in matching_runs:
                head_sha = run.get("headSha", "")
                if (
                    head_sha == target_sha
                    or head_sha.startswith(target_sha)
                    or target_sha.startswith(head_sha)
                ):
                    return WorkflowRun(
                        run_id=int(run["databaseId"]),
                        url=run.get("url", ""),
                        head_sha=head_sha,
                        event=run.get("event", ""),
                        status=run.get("status", ""),
                        created_at=parse_iso8601_utc(run["createdAt"]),
                    )

            # 2. If runs were registered at/after since but none matched target_sha,
            # then the dispatched run has a SHA mismatch.
            selected = matching_runs[0]
            head_sha = selected.get("headSha", "")
            run_id = int(selected["databaseId"])
            run_url = selected.get("url", "")
            raise HandledError(
                f"Target SHA mismatch for workflow '{workflow}': "
                f"expected '{target_sha}', but run {run_id} was dispatched with commit '{head_sha}'.",
                f"Ensure that branch '{ref}' points to commit '{target_sha}' before dispatching.",
                exit_code=EXIT_SHA_MISMATCH,
                details={
                    "expected_sha": target_sha,
                    "actual_sha": head_sha,
                    "run_id": run_id,
                    "url": run_url,
                },
            )

    raise HandledError(
        f"Timed out waiting for '{workflow}' workflow_dispatch run on commit '{target_sha}' to register.",
        f"Check Actions page on GitHub or verify that '{workflow}' can be run on '{ref}'.",
        exit_code=EXIT_FAILURE,
    )


def extract_page_url(
    workflow: str,
    jobs_data: list[dict[str, Any]],
    repo_dir: Path | None = None,
) -> str | None:
    """Attempts to extract the deployed GitHub Pages URL."""
    has_pages_job = False
    for job in jobs_data:
        name = job.get("name", "").lower()
        if "page" in name or "deploy gallery" in name or "visual" in name:
            has_pages_job = True
            break
        for step in job.get("steps", []):
            step_name = step.get("name", "").lower()
            if "deploy to github pages" in step_name or "upload pages" in step_name:
                has_pages_job = True
                break

    if not has_pages_job and workflow != "nightly-visual-snapshot.yml":
        return None

    # Query GitHub Pages endpoint
    effective_repo_dir = repo_dir or Path.cwd()
    owner, repo = resolve_repo_owner_and_name(effective_repo_dir)
    res = _run_gh_cmd(
        ["api", f"repos/{owner}/{repo}/pages", "--jq", ".html_url"],
        repo_dir=repo_dir,
        check=False,
    )
    if res.returncode == 0 and res.stdout.strip():
        return res.stdout.strip()

    return f"https://{owner}.github.io/{repo}/"


def check_superseded_by(
    run_id: int,
    workflow: str = "",
    ref: str = "main",
    run_created_at: datetime | None = None,
    repo_dir: Path | None = None,
) -> int | None:
    """Checks if a cancelled run was superseded by a newer run on the same ref."""
    cmd = ["run", "list"]
    if workflow:
        cmd.append(f"--workflow={workflow}")
    if ref:
        cmd.append(f"--branch={ref}")
    cmd.extend(["--limit=5", "--json=databaseId,createdAt"])

    res = _run_gh_cmd(
        cmd,
        repo_dir=repo_dir,
        check=False,
    )
    if res.returncode != 0:
        return None
    try:
        runs = json.loads(res.stdout)
        for r in runs:
            r_id = int(r["databaseId"])
            if r_id != run_id:
                if run_created_at:
                    try:
                        r_dt = parse_iso8601_utc(r.get("createdAt", ""))
                        if r_dt > run_created_at:
                            return r_id
                    except Exception:
                        pass
                else:
                    return r_id
    except Exception:
        pass
    return None


def watch_run(
    run_id: int,
    workflow: str = "",
    ref: str = "main",
    poll_interval_s: float = 30.0,
    timeout_s: float = 2400.0,
    repo_dir: Path | None = None,
) -> RunOutcome:
    """Polls gh run view until completion and classifies outcome, failed jobs, and URLs."""
    start_time = time.monotonic()

    while True:
        res = _run_gh_cmd(
            [
                "run",
                "view",
                str(run_id),
                "--json=databaseId,headSha,event,status,conclusion,url,createdAt,jobs",
            ],
            repo_dir=repo_dir,
            err_prefix=f"Failed to inspect run {run_id}",
            instruction="Ensure you have network access and valid GitHub credentials.",
            check=False,
        )
        if res.returncode == 0:
            try:
                data = json.loads(res.stdout)
            except json.JSONDecodeError:
                data = {}
        else:
            data = {}

        status = data.get("status")
        if status == "completed":
            conclusion = data.get("conclusion") or "unknown"
            jobs = data.get("jobs", [])

            failed_jobs: list[FailedJobSummary] = []
            for j in jobs:
                j_conc = j.get("conclusion") or ""
                if j_conc in ("failure", "timed_out", "action_required", "startup_failure"):
                    failed_steps = [
                        s.get("name", "")
                        for s in j.get("steps", [])
                        if s.get("conclusion") in ("failure", "timed_out", "action_required")
                    ]
                    failed_jobs.append(
                        {
                            "name": j.get("name", ""),
                            "conclusion": j_conc,
                            "failed_steps": failed_steps,
                            "url": j.get("url", ""),
                        }
                    )

            page_url = extract_page_url(workflow, jobs, repo_dir=repo_dir)

            superseded_by: int | None = None
            if conclusion == "cancelled":
                run_dt = None
                if data.get("createdAt"):
                    try:
                        run_dt = parse_iso8601_utc(data["createdAt"])
                    except Exception:
                        pass
                superseded_by = check_superseded_by(
                    run_id, workflow, ref, run_created_at=run_dt, repo_dir=repo_dir
                )

            return RunOutcome(
                run_id=run_id,
                url=data.get("url", f"https://github.com/google/xmc/actions/runs/{run_id}"),
                head_sha=data.get("headSha", ""),
                event=data.get("event", "workflow_dispatch"),
                conclusion=conclusion,
                superseded_by=superseded_by,
                failed_jobs=failed_jobs,
                page_url=page_url,
            )

        elapsed = time.monotonic() - start_time
        if elapsed >= timeout_s:
            raise HandledError(
                f"Timed out watching run {run_id} for workflow '{workflow}' after {timeout_s:.0f}s.",
                f"Inspect the run directly at https://github.com/google/xmc/actions/runs/{run_id}.",
                exit_code=EXIT_FAILURE,
            )

        time.sleep(poll_interval_s)


def update_state_file(
    state_file_path: Path,
    target_sha: str,
    workflow: str,
    run_info: dict[str, Any],
) -> None:
    """Persists release workflow execution state to a JSON file."""
    state_file_path.parent.mkdir(parents=True, exist_ok=True)
    existing_state: dict[str, Any] = {}
    if state_file_path.exists():
        try:
            with open(state_file_path, encoding="utf-8") as f:
                existing_state = json.load(f)
        except Exception:
            existing_state = {}

    existing_state["target_sha"] = target_sha
    runs_map = existing_state.setdefault("runs", {})
    runs_map[workflow] = run_info
    existing_state["last_run"] = {"workflow": workflow, **run_info}

    temp_path = state_file_path.with_suffix(".tmp")
    with open(temp_path, "w", encoding="utf-8") as f:
        json.dump(existing_state, f, indent=2)
    temp_path.replace(state_file_path)


def dispatch_and_watch(args: argparse.Namespace) -> DispatchWatchResult:
    """Executes the dispatch, poll, verification, and watch workflow."""
    repo_dir = Path(args.repo_dir).resolve()
    workflow = args.workflow.strip()
    if not workflow:
        raise HandledError(
            "Workflow name/file cannot be empty.",
            "Provide a valid workflow filename (e.g. '--workflow nightly-build.yml').",
            exit_code=EXIT_FAILURE,
        )

    ref = (getattr(args, "ref", None) or "main").strip()
    target_sha = (getattr(args, "target_sha", None) or getattr(args, "sha", None) or "").strip()
    if not target_sha:
        # Resolve target commit from ref
        target_sha = resolve_target_commit(f"origin/{ref}", repo_dir)

    actor = (getattr(args, "actor", None) or "").strip()
    if not actor:
        actor = resolve_actor(repo_dir)

    # 1. Resumption check with --find-existing
    if args.find_existing:
        existing_run = find_existing_run(workflow, target_sha, ref=ref, repo_dir=repo_dir)
        if existing_run:
            outcome = watch_run(
                existing_run.run_id,
                workflow=workflow,
                ref=ref,
                poll_interval_s=1.0,
                timeout_s=30.0,
                repo_dir=repo_dir,
            )
            if args.state_file:
                update_state_file(
                    Path(args.state_file).resolve(),
                    target_sha=target_sha,
                    workflow=workflow,
                    run_info={
                        "run_id": outcome.run_id,
                        "url": outcome.url,
                        "conclusion": outcome.conclusion,
                        "reused_existing": True,
                    },
                )
            return {
                "run_id": outcome.run_id,
                "url": outcome.url,
                "head_sha": outcome.head_sha,
                "event": outcome.event,
                "conclusion": outcome.conclusion,
                "superseded_by": outcome.superseded_by,
                "failed_jobs": outcome.failed_jobs,
                "page_url": outcome.page_url,
                "_exit_code": EXIT_SUCCESS,
            }

    # 2. Dispatch workflow run
    since = dispatch_workflow(workflow, ref=ref, repo_dir=repo_dir)

    # 3. Poll and verify commit SHA
    try:
        run = find_dispatched_run(
            workflow,
            target_sha=target_sha,
            since=since,
            actor=actor,
            ref=ref,
            poll_interval_s=args.poll_interval,
            repo_dir=repo_dir,
        )
    except HandledError as e:
        if e.exit_code == EXIT_SHA_MISMATCH and e.details:
            run_id = int(e.details.get("run_id", 0))
            actual_sha = str(e.details.get("actual_sha", ""))
            run_url = str(
                e.details.get("url", f"https://github.com/google/xmc/actions/runs/{run_id}")
            )
            if args.state_file:
                update_state_file(
                    Path(args.state_file).resolve(),
                    target_sha=target_sha,
                    workflow=workflow,
                    run_info={
                        "run_id": run_id,
                        "url": run_url,
                        "conclusion": "sha_mismatch",
                    },
                )
            return {
                "run_id": run_id,
                "url": run_url,
                "head_sha": actual_sha,
                "event": "workflow_dispatch",
                "conclusion": "sha_mismatch",
                "superseded_by": None,
                "failed_jobs": [],
                "page_url": None,
                "_exit_code": EXIT_SHA_MISMATCH,
            }
        raise

    if args.state_file:
        update_state_file(
            Path(args.state_file).resolve(),
            target_sha=target_sha,
            workflow=workflow,
            run_info={
                "run_id": run.run_id,
                "url": run.url,
                "conclusion": "in_progress",
            },
        )

    # 4. If --no-watch, return immediately
    if not getattr(args, "watch", True):
        return {
            "run_id": run.run_id,
            "url": run.url,
            "head_sha": run.head_sha,
            "event": run.event,
            "conclusion": "in_progress",
            "superseded_by": None,
            "failed_jobs": [],
            "page_url": None,
            "_exit_code": EXIT_SUCCESS,
        }

    # 5. Watch to completion
    outcome = watch_run(
        run.run_id,
        workflow=workflow,
        ref=ref,
        poll_interval_s=args.poll_interval,
        timeout_s=args.timeout,
        repo_dir=repo_dir,
    )

    if args.state_file:
        update_state_file(
            Path(args.state_file).resolve(),
            target_sha=target_sha,
            workflow=workflow,
            run_info={
                "run_id": outcome.run_id,
                "url": outcome.url,
                "conclusion": outcome.conclusion,
            },
        )

    # Map conclusion to distinct exit codes
    if outcome.conclusion == "success":
        exit_code = EXIT_SUCCESS
    elif outcome.conclusion == "cancelled":
        exit_code = EXIT_CANCELLED
    else:
        exit_code = EXIT_FAILURE

    return {
        "run_id": outcome.run_id,
        "url": outcome.url,
        "head_sha": outcome.head_sha,
        "event": outcome.event,
        "conclusion": outcome.conclusion,
        "superseded_by": outcome.superseded_by,
        "failed_jobs": outcome.failed_jobs,
        "page_url": outcome.page_url,
        "_exit_code": exit_code,
    }


def create_parser() -> argparse.ArgumentParser:
    """Constructs the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Dispatches a GitHub Actions workflow, polls for registration, and monitors outcome."
    )
    parser.add_argument(
        "--workflow",
        "-w",
        required=True,
        help="Target workflow filename or identifier (e.g. 'nightly-build.yml').",
    )
    parser.add_argument(
        "--target-sha",
        "--sha",
        "-s",
        default="",
        help="Expected commit SHA for the workflow run (defaults to origin/<ref>).",
    )
    parser.add_argument(
        "--ref",
        "-r",
        default="main",
        help="Git branch or ref to trigger workflow on (default: 'main').",
    )
    parser.add_argument(
        "--actor",
        "-a",
        default="",
        help="GitHub username/actor who dispatched the workflow (defaults to authenticated user).",
    )
    parser.add_argument(
        "--find-existing",
        action="store_true",
        help="Check for an existing successful run on target SHA before dispatching.",
    )
    parser.add_argument(
        "--state-file",
        help="Path to JSON state file to persist workflow run metadata across multi-turn sessions.",
    )
    parser.add_argument(
        "--watch",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="Watch workflow run until completion (default: --watch, use --no-watch to exit immediately).",
    )
    parser.add_argument(
        "--poll-interval",
        type=float,
        default=5.0,
        help="Polling interval in seconds (default: 5.0).",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=2400.0,
        help="Maximum timeout in seconds for watching workflow run completion (default: 2400.0).",
    )
    parser.add_argument(
        "--repo-dir",
        default=".",
        help="Path to repository root (default: '.').",
    )
    return parser


def main() -> None:
    """CLI entrypoint."""
    parser = create_parser()
    run_tool(parser, dispatch_and_watch)


if __name__ == "__main__":
    main()
