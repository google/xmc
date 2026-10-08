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

"""Runs the Bazel unit tests affected by a change, scoped the same way as CI.

tools/benchmark/resolve_test_scope.py resolves the scope in-process: --local (default)
scopes the changes since the merge base of --base-ref and HEAD, uncommitted and untracked
files included; --ci scopes the GitHub Actions event. After a Spanner emulator check, the
affected targets run under `bazel test` with the CI test flags and any --bazel-flag. Bazel
output streams to stderr; stdout carries only the JSON result. Suites that run outside
Bazel (UI, Helm chart) are listed by CI action under other_suites, not run.

Exit codes: 0 = tests passed, dry run, or nothing to run; 1 = handled error (test
failures, unreachable Spanner emulator, git problems); 2 = unhandled error.
"""

import argparse
import dataclasses
import functools
import json
import os
import socket
import sys
import tempfile
import time
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any
from urllib.parse import unquote, urlparse

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.benchmark import resolve_test_scope  # noqa: E402
from tools.common.tool_utils import HandledError, log_info, run_command, run_tool  # noqa: E402

# Test flags of the CI `bazel test` step; add CI-only cache and timeout flags with --bazel-flag.
BAZEL_TEST_FLAGS: tuple[str, ...] = (
    "--test_output=errors",
    "--experimental_ui_max_stdouterr_bytes=2097152",
    "--build_tests_only",
    "--test_timeout=60,300,300,300",
)
PASSED_STATUSES = frozenset({"PASSED", "FLAKY"})
# Test statuses of targets that never ran, e.g. because the build failed first.
NOT_RUN_STATUSES = frozenset({"NO_STATUS", "TOOL_HALTED_BEFORE_TESTING"})

# The variable and default address that internal/storage/spanner/emulator dials.
EMULATOR_HOST_ENV = "SPANNER_EMULATOR_HOST"
DEFAULT_EMULATOR_HOST = "localhost:9010"
EMULATOR_CONNECT_TIMEOUT_SECONDS = 1.0

# CI actions of the suites that run outside Bazel, keyed by the resolver output selecting them.
OTHER_SUITE_ACTIONS: dict[str, str] = {
    "run_ui": ".github/actions/run-ui-tests/action.yml",
    "run_helm": ".github/actions/run-chart-tests/action.yml",
}


@dataclasses.dataclass(frozen=True)
class FailedTarget:
    """A test target that did not pass, with the test logs of its failed attempts."""

    target: str
    status: str
    test_logs: tuple[str, ...] = ()


@dataclasses.dataclass(frozen=True)
class BazelTestOutcome:
    """Results of a `bazel test` run, read from its Build Event Protocol file."""

    exit_code: int
    exit_name: str
    passed_targets: tuple[str, ...]
    failed_targets: tuple[FailedTarget, ...]
    not_run_targets: tuple[str, ...]
    build_errors: tuple[str, ...]


def resolve_scope(args: argparse.Namespace, env: Mapping[str, str]) -> dict[str, Any]:
    """Returns the resolver's scope of the local checkout or, with --ci, of the CI event.

    Raises:
        ChangedFilesError: If git cannot list the local changes.
    """
    if not args.ci:
        return resolve_test_scope.resolve_local_scope(
            args.base_ref or resolve_test_scope.DEFAULT_LOCAL_BASE_REF,
            _REPO_ROOT,
            repository_cache=args.repository_cache,
        )
    resolver_argv = ["--base", args.base_ref] if args.base_ref else []
    if args.repository_cache:
        resolver_argv += ["--repository-cache", args.repository_cache]
    resolver_args = resolve_test_scope.build_parser().parse_args(resolver_argv)
    return resolve_test_scope.resolve_ci_scope(resolver_args, env)


def describe_scope(args: argparse.Namespace, scope: Mapping[str, Any]) -> dict[str, Any]:
    """Builds the JSON report fields that describe the resolved scope."""
    return {
        "mode": "ci" if args.ci else "local",
        "changed_files": scope.get("changed_files"),
        "backend_mode": scope["backend_mode"],
        "reason": scope["reason"],
        "bazel_targets": str(scope["bazel_targets"]).split(),
        "other_suites": [action for key, action in OTHER_SUITE_ACTIONS.items() if scope[key]],
        "dry_run": args.dry_run,
    }


def parse_emulator_address(address: str) -> tuple[str, int]:
    """Splits an emulator address such as localhost:9010 or [::1]:9010 into host and port.

    Raises:
        HandledError: If address is not HOST:PORT with a port from 1 to 65535.
    """
    host, _, port = address.rpartition(":")
    host = host.removeprefix("[").removesuffix("]")
    if host and port.isdecimal() and 0 < int(port) < 65536:
        return host, int(port)
    raise HandledError(
        f"{EMULATOR_HOST_ENV}={address!r} is not a HOST:PORT address.",
        f"Set {EMULATOR_HOST_ENV} to the emulator address, e.g. {DEFAULT_EMULATOR_HOST}.",
    )


def wait_for_port(host: str, port: int, wait_seconds: float) -> bool:
    """Returns whether host:port accepts a TCP connection, retrying for up to wait_seconds."""
    deadline = time.monotonic() + wait_seconds
    while True:
        try:
            with socket.create_connection((host, port), timeout=EMULATOR_CONNECT_TIMEOUT_SECONDS):
                return True
        except OSError:
            if time.monotonic() >= deadline:
                return False
            time.sleep(1)


def ensure_emulator_reachable(env: Mapping[str, str], wait_seconds: float) -> None:
    """Fails fast unless the Spanner emulator at SPANNER_EMULATOR_HOST accepts connections.

    Raises:
        HandledError: If the address is malformed or nothing listens on it in time.
    """
    address = env.get(EMULATOR_HOST_ENV) or DEFAULT_EMULATOR_HOST
    if wait_for_port(*parse_emulator_address(address), wait_seconds):
        return
    raise HandledError(
        f"Nothing accepts connections on the Spanner emulator address {address}.",
        "Start the emulator as described in 'Running Spanner Database Health Unit Tests' in "
        f"docs/developer.md, set {EMULATOR_HOST_ENV} if it listens elsewhere, or re-run with "
        "--skip-emulator-check if the affected tests do not use Spanner.",
    )


def build_bazel_test_command(
    targets: Sequence[str], extra_flags: Sequence[str], env: Mapping[str, str], bep_path: Path
) -> list[str]:
    """Builds the `bazel test` command line with the CI test flags and a BEP JSON file.

    extra_flags follow the defaults, so they win wherever Bazel keeps the last value. Bazel
    runs tests without the client environment, so a set SPANNER_EMULATOR_HOST is passed on
    with --test_env.
    """
    emulator_host = env.get(EMULATOR_HOST_ENV)
    test_env = [f"--test_env={EMULATOR_HOST_ENV}={emulator_host}"] if emulator_host else []
    bep_flag = f"--build_event_json_file={bep_path}"
    return ["bazel", "test", *BAZEL_TEST_FLAGS, *test_env, bep_flag, *extra_flags, "--", *targets]


def file_uri_to_path(uri: str) -> str:
    """Converts a file:// URI from the Build Event Protocol to a local path."""
    return unquote(urlparse(uri).path) if uri.startswith("file://") else uri


def summarize_statuses(
    statuses: Mapping[str, str], failed_logs: Mapping[str, Sequence[str]]
) -> tuple[tuple[str, ...], tuple[FailedTarget, ...], tuple[str, ...]]:
    """Splits per-target test statuses into passed, failed, and never-run targets."""
    sorted_statuses = sorted(statuses.items())
    passed = tuple(label for label, status in sorted_statuses if status in PASSED_STATUSES)
    not_run = tuple(label for label, status in sorted_statuses if status in NOT_RUN_STATUSES)
    failed = tuple(
        FailedTarget(label, status, tuple(failed_logs.get(label, ())))
        for label, status in sorted_statuses
        if status not in PASSED_STATUSES | NOT_RUN_STATUSES
    )
    return passed, failed, not_run


def read_build_events(bep_path: Path) -> list[dict[str, Any]]:
    """Returns the events of a Build Event Protocol JSON file, skipping malformed lines."""
    lines = bep_path.read_text(encoding="utf-8").splitlines() if bep_path.exists() else []
    events = []
    for line in filter(str.strip, lines):
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:  # e.g. the last line of an interrupted Bazel run
            log_info(f"Skipped a malformed line of {bep_path}.", stream=sys.stderr)
    return events


def read_test_outcome(bep_path: Path, exit_code: int) -> BazelTestOutcome:
    """Reads the outcome of a `bazel test` run from its Build Event Protocol JSON file.

    Targets that failed to build get no testSummary event, only an unsuccessful
    targetCompleted event; loading and analysis errors arrive as aborted events.
    """
    statuses: dict[str, str] = {}
    failed_logs: dict[str, list[str]] = {}
    build_errors: list[str] = []
    exit_name = "UNKNOWN"
    for event in read_build_events(bep_path):
        event_id = event.get("id", {})
        if "testSummary" in event:
            label, summary = event_id["testSummary"]["label"], event["testSummary"]
            statuses[label] = summary.get("overallStatus", "NO_STATUS")
            failed_logs[label] = [file_uri_to_path(f["uri"]) for f in summary.get("failed", [])]
        elif "completed" in event and not event["completed"].get("success"):
            statuses.setdefault(event_id["targetCompleted"]["label"], "FAILED_TO_BUILD")
        elif description := event.get("aborted", {}).get("description"):
            build_errors.append(description)
        elif "finished" in event:
            exit_name = event["finished"].get("exitCode", {}).get("name", exit_name)
    passed, failed, not_run = summarize_statuses(statuses, failed_logs)
    return BazelTestOutcome(exit_code, exit_name, passed, failed, not_run, tuple(build_errors))


def run_bazel_tests(
    targets: Sequence[str], extra_flags: Sequence[str], env: Mapping[str, str]
) -> BazelTestOutcome:
    """Runs `bazel test` with its output streamed to stderr and reads the per-target results.

    Raises:
        HandledError: If bazel is not installed.
    """
    with tempfile.TemporaryDirectory(prefix="run_affected_tests_") as tmp_dir:
        bep_path = Path(tmp_dir) / "build_events.json"
        cmd = build_bazel_test_command(targets, extra_flags, env, bep_path)
        try:
            proc = run_command(cmd, check=False, cwd=_REPO_ROOT, stream=sys.stderr)
        except FileNotFoundError as e:
            raise HandledError(
                "`bazel` is not installed or not on PATH.",
                "Install Bazel, for example through Bazelisk, and re-run.",
            ) from e
        return read_test_outcome(bep_path, proc.returncode)


def raise_for_test_failure(outcome: BazelTestOutcome, report: Mapping[str, Any]) -> None:
    """Raises HandledError with the report and the outcome unless `bazel test` succeeded."""
    if outcome.exit_code == 0:
        return
    failed = ", ".join(f"{f.target} ({f.status})" for f in outcome.failed_targets)
    raise HandledError(
        f"bazel test failed with {outcome.exit_name} (exit code {outcome.exit_code}); "
        f"failed targets: {failed or 'none, see build_errors'}; "
        f"targets that did not run: {len(outcome.not_run_targets)}.",
        "Fix the failures using the test logs and build errors in details and the Bazel output "
        "on stderr, then re-run python3 ./tools/run_affected_tests.py.",
        details={**report, **dataclasses.asdict(outcome)},
    )


def run_affected_tests(args: argparse.Namespace, env: Mapping[str, str]) -> dict[str, Any]:
    """Resolves the affected test scope and, unless --dry-run, runs it with Bazel."""
    report = describe_scope(args, resolve_scope(args, env))
    log_info(f"Backend scope: {report['backend_mode']} ({report['reason']}).", stream=sys.stderr)
    for action in report["other_suites"]:
        log_info(f"CI also runs {action} for these changes.", stream=sys.stderr)
    if args.dry_run or not report["bazel_targets"]:
        return report | {"passed_targets": []}
    if not args.skip_emulator_check:
        ensure_emulator_reachable(env, args.emulator_wait_seconds)
    cache_flag = [f"--repository_cache={args.repository_cache}"] if args.repository_cache else []
    outcome = run_bazel_tests(report["bazel_targets"], [*cache_flag, *args.bazel_flags], env)
    raise_for_test_failure(outcome, report)
    return report | {"passed_targets": list(outcome.passed_targets)}


def build_parser() -> argparse.ArgumentParser:
    """Builds the command-line parser."""
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--local", action="store_true", help="Scope the local checkout (default).")
    modes.add_argument("--ci", action="store_true", help="Scope the GitHub Actions event.")
    parser.add_argument(
        "--base-ref", help="Comparison base (default: origin/main, or the event's base with --ci)."
    )
    parser.add_argument(
        "--repository-cache", help="Bazel repository cache directory for the scope query and tests."
    )
    parser.add_argument(
        "--bazel-flag",
        action="append",
        default=[],
        dest="bazel_flags",
        metavar="FLAG",
        help="Extra `bazel test` flag, written as --bazel-flag=FLAG; repeatable; wins on conflict.",
    )
    parser.add_argument(
        "--emulator-wait-seconds",
        type=float,
        default=0.0,
        help="Retry the Spanner emulator check for up to this long (default: 0, one attempt).",
    )
    parser.add_argument(
        "--skip-emulator-check", action="store_true", help="Do not check the Spanner emulator."
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="Print the resolved scope without running tests."
    )
    return parser


def main() -> None:
    """Entry point for the run_affected_tests tool."""
    run_tool(build_parser(), functools.partial(run_affected_tests, env=os.environ))


if __name__ == "__main__":
    main()
