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
treated as in-progress rather than failure.

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

from tools.common.tool_utils import HandledError, log_info, run_tool  # noqa: E402

DEFAULT_TIMEOUT_SECONDS = 1800
DEFAULT_INTERVAL_SECONDS = 30
MAX_INTERVAL_SECONDS = 120
DEFAULT_LOG_LINES = 80
MAX_CONSECUTIVE_GH_ERRORS = 3

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
    """Extracts `(owner/repo, run_id, job_id)` from a GitHub Actions details URL."""
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


def fetch_checks(pr: str, repo: str | None) -> list[CheckResult]:
    """Fetches and normalizes the PR's status check rollup via gh.

    Raises:
      subprocess.CalledProcessError: When gh exits non-zero.
      json.JSONDecodeError: When gh prints invalid JSON.
      MalformedResponseError: When gh prints JSON that is not an object.
    """
    cmd = ["gh", "pr", "view", pr, "--json", "statusCheckRollup"]
    if repo:
        cmd += ["--repo", repo]
    res = subprocess.run(cmd, capture_output=True, text=True, errors="replace", check=True)
    data = json.loads(res.stdout)
    if not isinstance(data, dict):
        raise MalformedResponseError(f"expected a JSON object, got {type(data).__name__}")
    rollup = data.get("statusCheckRollup") or []
    return [classify_check(entry) for entry in rollup]


def fetch_job_log_tail(run_repo: str, job_id: str, max_lines: int) -> str:
    """Returns a bounded, error-anchored tail of one Actions job's log.

    Uses the REST job-logs endpoint because `gh run view --log-failed` silently
    prints nothing on some gh releases (observed on gh 2.46).
    """
    cmd = ["gh", "api", f"repos/{run_repo}/actions/jobs/{job_id}/logs"]
    res = subprocess.run(cmd, capture_output=True, text=True, errors="replace", check=False)
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
    """Describes one failed check, including its run/job IDs and log tail."""
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


def _poll_once(args: argparse.Namespace, gh_errors: int) -> tuple[list[CheckResult] | None, int]:
    """Fetches checks once, tolerating up to MAX_CONSECUTIVE_GH_ERRORS transient errors."""
    try:
        return fetch_checks(args.pr, args.repo), 0
    except (subprocess.CalledProcessError, json.JSONDecodeError, MalformedResponseError) as e:
        gh_errors += 1
        stderr = getattr(e, "stderr", "") or str(e)
        if gh_errors >= MAX_CONSECUTIVE_GH_ERRORS:
            raise HandledError(
                f"gh pr view failed {gh_errors} times in a row: {stderr.strip()}",
                "Verify the PR identifier, --repo value, and `gh auth status`.",
            ) from e
        log_info(
            f"Transient gh error ({gh_errors}/{MAX_CONSECUTIVE_GH_ERRORS}): {stderr.strip()}",
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
        if polled is not None:
            checks = polled
            current_names = _completed_names(checks)
            if _is_settled(current_names, previous_names):
                return _report_result(args, checks)
            previous_names = current_names
            log_info(f"PR {args.pr} checks: {summarize_counts(checks)}", stream=sys.stderr)
        remaining = deadline - clock()
        if remaining <= 0:
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
        "--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="Max seconds to wait."
    )
    parser.add_argument(
        "--interval",
        type=int,
        default=DEFAULT_INTERVAL_SECONDS,
        help=f"Initial poll interval in seconds; doubles up to {MAX_INTERVAL_SECONDS}s.",
    )
    parser.add_argument(
        "--log-lines",
        type=int,
        default=DEFAULT_LOG_LINES,
        help="Max log lines printed per failed job.",
    )
    return parser


def main():
    """Main entrypoint for wait_for_ci tool."""
    run_tool(build_parser(), wait_for_ci)


if __name__ == "__main__":
    main()
