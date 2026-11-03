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

"""Waits for all CI checks on a Pull Request to finish and summarizes failures.

Polls `gh pr view <pr> --json statusCheckRollup` (supported by every gh release,
unlike `gh pr checks --json`) with exponential backoff. Unlike bare
`gh pr checks`, which exits 8 while checks are pending, pending checks are
treated as in-progress rather than failure. Only the newest run of each check
is judged, so runs cancelled because a later push superseded them do not count
as failures.

Exit codes (via `run_tool`):
  0: All checks completed successfully (or were skipped/neutral).
  1: At least one check failed, or the timeout elapsed.
  2: Unexpected internal error.

On failure, the error JSON lists each failed check with the GitHub Actions run
and job IDs parsed from its details URL, plus a bounded tail of the job log
(ending at its last `##[error]` line) so callers never dump full CI logs.
"""

import argparse
import dataclasses
import json
import re
import subprocess
import sys
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import (  # noqa: E402
    HandledError,
    log_info,
    positive_int_arg,
    run_command,
    run_tool,
)

DEFAULT_TIMEOUT_SECONDS = 1800
DEFAULT_INTERVAL_SECONDS = 30
MAX_INTERVAL_SECONDS = 120
DEFAULT_LOG_LINES = 80
MAX_CONSECUTIVE_GH_ERRORS = 3
GH_COMMAND_TIMEOUT_SECONDS = 60

PENDING = "pending"
PASSED = "passed"
FAILED = "failed"

_PASSING_CONCLUSIONS = frozenset({"SUCCESS", "NEUTRAL", "SKIPPED"})
_PENDING_CONTEXT_STATES = frozenset({"PENDING", "EXPECTED"})
_RUN_URL_PATTERN = re.compile(r"github\.com/([^/]+/[^/]+)/actions/runs/(\d+)(?:/job/(\d+))?")
_ERROR_MARKER = "##[error]"


class MalformedResponseError(ValueError):
    """Raised when gh returns well-formed JSON with an unexpected shape."""


@dataclasses.dataclass(frozen=True)
class CheckResult:
    """Normalized view of a CheckRun or legacy StatusContext entry."""

    name: str
    state: str
    conclusion: str
    details_url: str


def classify_check(entry: dict[str, Any]) -> CheckResult:
    """Normalizes one `statusCheckRollup` entry into a CheckResult.

    Args:
      entry: A CheckRun (`status`/`conclusion`) or StatusContext (`state`) dict.

    Returns:
      The normalized CheckResult with state PENDING, PASSED, or FAILED.
    """
    if entry.get("__typename") == "StatusContext":
        raw = (entry.get("state") or "").upper()
        if raw in _PENDING_CONTEXT_STATES:
            state = PENDING
        else:
            state = PASSED if raw == "SUCCESS" else FAILED
        return CheckResult(entry.get("context") or "", state, raw, entry.get("targetUrl") or "")

    conclusion = (entry.get("conclusion") or "").upper()
    if (entry.get("status") or "").upper() != "COMPLETED":
        state = PENDING
    else:
        state = PASSED if conclusion in _PASSING_CONCLUSIONS else FAILED
    return CheckResult(entry.get("name") or "", state, conclusion, entry.get("detailsUrl") or "")


def parse_run_reference(details_url: str) -> tuple[str, str, str | None] | None:
    """Extracts the repository, run ID, and job ID from a GitHub Actions URL.

    Args:
      details_url: A check's details URL, e.g.
        `https://github.com/<owner>/<repo>/actions/runs/<run_id>/job/<job_id>`.

    Returns:
      `(owner/repo, run_id, job_id)` with `job_id` None when the URL has no
      job segment, or None when the URL is not a GitHub Actions run URL.
    """
    match = _RUN_URL_PATTERN.search(details_url)
    if not match:
        return None
    return match.group(1), match.group(2), match.group(3)


def trim_job_log(text: str, max_lines: int) -> str:
    """Returns at most `max_lines` lines ending at the last `##[error]` line.

    Ending at the last error marker drops trailing post-job cleanup noise; when
    no marker exists, the plain tail of the log is returned.
    """
    lines = text.splitlines()
    error_indexes = [i for i, line in enumerate(lines) if _ERROR_MARKER in line]
    end = error_indexes[-1] + 1 if error_indexes else len(lines)
    return "\n".join(lines[max(0, end - max_lines) : end])


def parse_rollup(stdout: str) -> list[dict[str, Any]]:
    """Parses `gh pr view --json statusCheckRollup` output into rollup entries.

    Args:
      stdout: Raw JSON printed by gh.

    Returns:
      The list of rollup entry dicts (empty when no checks are reported).

    Raises:
      json.JSONDecodeError: When stdout is not valid JSON.
      MalformedResponseError: When the JSON does not have the expected shape.
    """
    data = json.loads(stdout)
    if not isinstance(data, dict):
        raise MalformedResponseError(f"expected a JSON object, got {type(data).__name__}")
    rollup = data.get("statusCheckRollup") or []
    if not isinstance(rollup, list) or not all(isinstance(e, dict) for e in rollup):
        raise MalformedResponseError("expected statusCheckRollup to be a list of objects")
    return rollup


def _check_identity(entry: dict[str, Any]) -> tuple[str, str, str]:
    """Returns the key shared by repeated runs of the same check."""
    if entry.get("__typename") == "StatusContext":
        return ("StatusContext", "", entry.get("context") or "")
    return ("CheckRun", entry.get("workflowName") or "", entry.get("name") or "")


def _recency_key(entry: dict[str, Any]) -> tuple[int, int, bool, str, str]:
    """Orders runs of one check from oldest to newest by creation order.

    GitHub Actions run and job IDs increase monotonically and exist as soon as a
    job is queued. Run IDs order first, so the newest push decides: a hung older
    run cannot shadow a newer one, and a run-only URL (job ID 0) still ranks by
    its run. Within a run, a re-run job gets a newer job ID and wins. Trade-off:
    manually re-running an older run's job does not override a newer run.
    Checks without an Actions URL fall back to ranking unfinished runs newest,
    then ISO-8601 UTC timestamps, which compare lexically.
    """
    ref = parse_run_reference(entry.get("detailsUrl") or entry.get("targetUrl") or "")
    run_id, job_id = (int(ref[1]), int(ref[2] or 0)) if ref else (0, 0)
    return (
        run_id,
        job_id,
        classify_check(entry).state == PENDING,
        entry.get("startedAt") or "",
        entry.get("completedAt") or "",
    )


def latest_runs(rollup: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Keeps only the newest run of each check, in first-seen order.

    A force-push or restack starts new workflow runs whose concurrency group
    cancels the runs for the previous push. Those CANCELLED runs stay in the
    rollup for the same head commit, so judging every entry would report them as
    failures. A cancelled run that is the newest run of its check still counts.

    The rollup does not expose a run's trigger event, so runs of the same job
    started by different events (e.g. `pull_request` and `workflow_dispatch`)
    on one commit collapse into a single check judged by its newest run.

    Args:
      rollup: Raw `statusCheckRollup` entries.

    Returns:
      One entry per check, keyed by workflow and check name (or status context).
    """
    latest: dict[tuple[str, str, str], dict[str, Any]] = {}
    for entry in rollup:
        key = _check_identity(entry)
        current = latest.get(key)
        if current is None or _recency_key(entry) >= _recency_key(current):
            latest[key] = entry
    return list(latest.values())


def _run_gh(cmd: list[str], *, check: bool) -> subprocess.CompletedProcess:
    """Runs one quiet, bounded gh call, capturing output and replacing bad bytes.

    Errors propagate raw (no handled_instruction) so callers can retry them.
    """
    return run_command(
        cmd,
        check=check,
        capture_output=True,
        log_on_error=False,
        log_cmd=False,
        timeout=GH_COMMAND_TIMEOUT_SECONDS,
        errors="replace",
    )


def fetch_checks(pr: str, repo: str | None) -> list[CheckResult]:
    """Fetches and normalizes the PR's status check rollup via gh.

    Raises:
      OSError: When the gh binary is missing or cannot be executed.
      subprocess.CalledProcessError: When gh exits non-zero.
      subprocess.TimeoutExpired: When gh does not finish in time.
      json.JSONDecodeError: When gh prints invalid JSON.
      MalformedResponseError: When gh prints JSON with an unexpected shape.
    """
    cmd = ["gh", "pr", "view", pr, "--json", "statusCheckRollup"]
    if repo:
        cmd += ["--repo", repo]
    res = _run_gh(cmd, check=True)
    return [classify_check(entry) for entry in latest_runs(parse_rollup(res.stdout))]


def fetch_job_log_tail(run_repo: str, job_id: str, max_lines: int) -> str:
    """Returns a bounded, error-anchored tail of one Actions job's log.

    Uses the REST job-logs endpoint because `gh run view --log-failed` silently
    prints nothing on some gh releases (observed on gh 2.46). Failures to fetch
    the log are reported as a `<...>` placeholder string instead of raising.
    """
    cmd = ["gh", "api", f"repos/{run_repo}/actions/jobs/{job_id}/logs"]
    try:
        res = _run_gh(cmd, check=False)
    except (OSError, subprocess.TimeoutExpired) as e:
        return f"<gh api job logs failed: {e}>"
    if res.returncode != 0:
        return f"<gh api job logs failed (exit {res.returncode}): {res.stderr.strip()}>"
    return trim_job_log(res.stdout, max_lines)


def summarize_counts(checks: list[CheckResult]) -> dict[str, int]:
    """Counts checks per state."""
    counts = {PENDING: 0, PASSED: 0, FAILED: 0}
    for check in checks:
        counts[check.state] += 1
    return counts


def describe_failed_check(check: CheckResult, max_lines: int) -> dict[str, Any]:
    """Describes one failed check, including its run/job IDs and log tail.

    Args:
      check: A check whose state is FAILED.
      max_lines: Maximum number of job-log lines to include.

    Returns:
      A JSON-serializable dict with `name`, `conclusion`, `details_url`,
      `run_id`, `job_id`, and `log_tail` (None for non-Actions checks).
    """
    ref = parse_run_reference(check.details_url)
    run_id: str | None = None
    job_id: str | None = None
    log_tail: str | None = None
    if ref:
        run_repo, run_id, job_id = ref
        if job_id:
            log_tail = fetch_job_log_tail(run_repo, job_id, max_lines)
    return {
        "name": check.name,
        "conclusion": check.conclusion,
        "details_url": check.details_url,
        "run_id": run_id,
        "job_id": job_id,
        "log_tail": log_tail,
    }


def build_failure_details(checks: list[CheckResult], max_lines: int) -> dict[str, Any]:
    """Builds the failure report for all failed checks."""
    failed = [describe_failed_check(c, max_lines) for c in checks if c.state == FAILED]
    return {"outcome": "failure", "failed_checks": failed}


_TRANSIENT_GH_ERRORS = (
    subprocess.CalledProcessError,
    subprocess.TimeoutExpired,
    json.JSONDecodeError,
    MalformedResponseError,
)


def _error_text(error: Exception) -> str:
    """Returns gh's stderr for a failed call when available, else the error message."""
    stderr = getattr(error, "stderr", None)
    text = stderr if isinstance(stderr, str) and stderr.strip() else str(error)
    return text.strip()


def _poll_once(args: argparse.Namespace, gh_errors: int) -> tuple[list[CheckResult] | None, int]:
    """Fetches checks once, tolerating up to MAX_CONSECUTIVE_GH_ERRORS transient errors."""
    try:
        return fetch_checks(args.pr, args.repo), 0
    except OSError as e:
        raise HandledError(
            f"gh CLI could not be executed: {e}",
            "Install the GitHub CLI (https://cli.github.com), check PATH and file"
            " permissions, and run `gh auth login`.",
        ) from e
    except _TRANSIENT_GH_ERRORS as e:
        gh_errors += 1
        message = _error_text(e)
        if gh_errors >= MAX_CONSECUTIVE_GH_ERRORS:
            raise HandledError(
                f"gh pr view failed {gh_errors} times in a row: {message}",
                "Verify the PR identifier, --repo value, and `gh auth status`.",
            ) from e
        log_info(
            f"Transient gh error ({gh_errors}/{MAX_CONSECUTIVE_GH_ERRORS}): {message}",
            stream=sys.stderr,
        )
        return None, gh_errors


def _completed_names(checks: list[CheckResult]) -> frozenset[str] | None:
    """Returns the check-name set when all checks are complete, else None."""
    if not checks or any(c.state == PENDING for c in checks):
        return None
    return frozenset(c.name for c in checks)


def _is_settled(current: frozenset[str] | None, previous: frozenset[str] | None) -> bool:
    """True when two consecutive polls were fully complete with the same check set.

    Requiring two consecutive identical, fully-completed polls prevents a false
    pass in the gap between an early workflow finishing and later workflows
    registering their checks.
    """
    return current is not None and current == previous


def _raise_timeout(args: argparse.Namespace, checks: list[CheckResult] | None) -> None:
    """Raises a HandledError describing the timeout state."""
    if not checks:
        raise HandledError(
            f"No CI checks were reported for PR {args.pr} within {args.timeout}s.",
            "Confirm the branch was pushed and workflows are enabled, then rerun.",
            details={"outcome": "timeout"},
        )
    pending = sorted(c.name for c in checks if c.state == PENDING)
    raise HandledError(
        f"Timed out after {args.timeout}s with {len(pending)} check(s) still pending.",
        "Rerun wait_for_ci.py with a larger --timeout, or inspect the pending checks.",
        details={"outcome": "timeout", "pending_checks": pending},
    )


def _report_result(args: argparse.Namespace, checks: list[CheckResult]) -> dict[str, Any]:
    """Returns the success payload or raises a HandledError with failure details."""
    counts = summarize_counts(checks)
    if counts[FAILED] == 0:
        return {"outcome": "success", "pr": args.pr, "checks": counts}
    raise HandledError(
        f"{counts[FAILED]} CI check(s) failed on PR {args.pr}.",
        "Fix the failures shown in failed_checks[].log_tail, push, and rerun wait_for_ci.py.",
        details=build_failure_details(checks, args.log_lines),
    )


def wait_for_ci(
    args: argparse.Namespace,
    sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
) -> dict[str, Any]:
    """Polls PR checks with backoff until they settle, fail, or time out.

    Args:
      args: Parsed CLI arguments (pr, repo, timeout, interval, log_lines).
      sleep: Injectable sleep function (for tests).
      clock: Injectable monotonic clock (for tests).

    Returns:
      A success payload with per-state check counts.

    Raises:
      HandledError: When any check fails, gh repeatedly errors, or on timeout.
    """
    deadline = clock() + args.timeout
    interval, gh_errors = args.interval, 0
    checks, previous_names = None, None
    while True:
        polled, gh_errors = _poll_once(args, gh_errors)
        if polled is None:
            previous_names = None  # A failed poll breaks the "consecutive" chain.
        else:
            checks = polled
            current_names = _completed_names(checks)
            if _is_settled(current_names, previous_names):
                return _report_result(args, checks)
            previous_names = current_names
            log_info(f"PR {args.pr} checks: {summarize_counts(checks)}", stream=sys.stderr)
        remaining = deadline - clock()
        if remaining <= 0:
            if checks and _completed_names(checks) is not None:
                # Deadline hit before a confirming poll (or the confirming poll
                # failed): report the last fully-complete but unconfirmed result
                # rather than a misleading timeout.
                return _report_result(args, checks)
            _raise_timeout(args, checks)
        sleep(min(interval, remaining))
        interval = min(interval * 2, max(MAX_INTERVAL_SECONDS, args.interval))


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Wait for all CI checks on a GitHub PR and summarize failures."
    )
    parser.add_argument("pr", help="PR number, branch, or full GitHub PR URL.")
    parser.add_argument("--repo", help="Repository in owner/name form (default: current repo).")
    parser.add_argument(
        "--timeout",
        type=positive_int_arg,
        default=DEFAULT_TIMEOUT_SECONDS,
        help="Max seconds to wait.",
    )
    parser.add_argument(
        "--interval",
        type=positive_int_arg,
        default=DEFAULT_INTERVAL_SECONDS,
        help=f"Initial poll interval in seconds; doubles up to {MAX_INTERVAL_SECONDS}s.",
    )
    parser.add_argument(
        "--log-lines",
        type=positive_int_arg,
        default=DEFAULT_LOG_LINES,
        help="Max log lines printed per failed job.",
    )
    return parser


def main() -> None:
    """Main entrypoint for wait_for_ci tool."""
    run_tool(build_parser(), wait_for_ci)


if __name__ == "__main__":
    main()
