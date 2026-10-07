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

"""Runs the Bazel unit tests affected by local changes, scoped like PR CI.

Files changed since the merge base of --base-ref and HEAD (committed, staged,
unstaged, or untracked) are scoped by tools/benchmark/resolve_test_scope.py with pull
request semantics, and the resolved targets run under `bazel test` with the CI test
flags. If the resolver falls back to the full suite because its offline rdeps query
failed, external repositories are fetched once and the files are resolved again.
Tests that need the Spanner emulator fail fast when it is unreachable. Bazel output
goes to a log file (also streamed to stderr with --verbose or when stderr is a
terminal), per-target results come from the Build Event Protocol, and stdout carries
only the JSON result. The UI and Helm chart suites run outside Bazel, so when CI would
run them for the changes, the result lists them under other_suites with their CI
commands instead of running them.

Exit codes: 0 = tests passed, dry run, or nothing to run; 1 = handled error (test
failures, unreachable Spanner emulator, git or Bazel problems); 2 = unhandled error.
"""

import argparse
import dataclasses
import fnmatch
import functools
import json
import os
import shlex
import socket
import subprocess
import sys
import tempfile
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any
from urllib.parse import unquote, urlparse

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.benchmark.resolve_test_scope import RDEPS_QUERY_FAILED_REASON  # noqa: E402
from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

RESOLVER_PATH = _REPO_ROOT / "tools" / "benchmark" / "resolve_test_scope.py"

# Flags of the `bazel test` step in .github/actions/run-backend-tests/action.yml except the
# CI-only --config=ci, --remote_upload_local_results, and repository cache flags.
BAZEL_TEST_FLAGS: tuple[str, ...] = (
    "--test_output=errors",
    "--experimental_ui_max_stdouterr_bytes=2097152",
    "--build_tests_only",
    "--test_timeout=60,300,300,300",
)
PASSED_STATUSES = frozenset({"PASSED", "FLAKY"})
# Test statuses of targets that never ran, e.g. because the build failed before testing.
NOT_RUN_STATUSES = frozenset({"NO_STATUS", "TOOL_HALTED_BEFORE_TESTING"})
# Default targets of the CI `bazel test` step, which the resolver also uses as its full suite.
FULL_SUITE = ("//...", "-//cmd/ui/...")

# Why the full suite runs when the resolver reports RDEPS_QUERY_FAILED_REASON.
FALLBACK_EXPLANATION = (
    "resolve_test_scope.py could not narrow the scope because its offline (--fetch=false) "
    "rdeps query failed or timed out; run with --verbose to see its warning."
)
# Fetches the external repositories that the offline rdeps query needs; unlike
# `bazel fetch //...`, it also reaches repositories behind platform select() branches.
WARMUP_QUERY = "deps(//... except //cmd/ui/...)"
WARMUP_COMMAND = ("bazel", "query", "--keep_going", "--noimplicit_deps", WARMUP_QUERY)

# The override variable and default mirror internal/storage/spanner/emulator/emulator.go.
EMULATOR_HOST_ENV = "SPANNER_EMULATOR_HOST"
DEFAULT_EMULATOR_HOST = "localhost:9010"
EMULATOR_PACKAGE = "//internal/storage/spanner/emulator"
# The emulator image that .github/workflows/unit-tests.yml runs.
EMULATOR_IMAGE = "gcr.io/cloud-spanner-emulator/emulator:1.5.56"
EMULATOR_CONNECT_TIMEOUT_SECONDS = 1.0

# Commands of the .github/actions/run-ui-tests/action.yml steps, which CI runs in cmd/ui
# when the resolver reports run_ui.
UI_SUITE_COMMANDS = (
    "npm ci --legacy-peer-deps --prefer-offline --no-audit",
    "npm test -- --watch=false --browsers=ChromeHeadlessNoSandbox",
    "npx playwright install --with-deps chromium",
    "npm run build",
    "npm run test:screenshot",
)
# Paths filter of .github/actions/run-chart-tests/action.yml; its patterns are exact
# paths or end in /**, which fnmatch matches like the action's glob filter.
HELM_SUITE_PATH_PATTERNS = (
    "deploy/helm/**",
    "deploy/ctk_v2/xmanager-cloud.yaml",
    "test/helm/**",
    ".github/workflows/unit-tests.yml",
    ".github/actions/run-chart-tests/**",
)
# Test commands of that action, with its step env inlined and without the CI-only Helm
# install and `helm version` diagnostic.
HELM_SUITE_COMMANDS = (
    "helm lint deploy/helm",
    "XMC_REQUIRE_HELM=1 python3 -m unittest discover --start-directory test/helm "
    "--pattern '*_test.py' --verbose",
)

# Boolean command-line flags and their help texts.
SWITCHES: tuple[tuple[str, str], ...] = (
    ("--all", "Run the full backend suite (//... -//cmd/ui/...) instead of the affected targets."),
    ("--dry-run", "Print the resolved scope without running tests."),
    ("--no-warmup", "Skip the repository fetch and re-resolve after a failed rdeps query."),
    ("--skip-emulator-check", "Run tests even if the Spanner emulator they need is unreachable."),
    ("--verbose", "Stream Bazel and resolver output to stderr even if it is not a terminal."),
)


@dataclasses.dataclass(frozen=True)
class ResolvedScope:
    """Test scope as reported by resolve_test_scope.py: backend targets and the UI flag."""

    backend_mode: str
    reason: str
    bazel_targets: tuple[str, ...]
    run_ui: bool = False

    @property
    def fallback(self) -> bool:
        """Whether the resolver ran the full suite because its rdeps query failed."""
        return self.reason == RDEPS_QUERY_FAILED_REASON


NO_CHANGES_SCOPE = ResolvedScope("none", "no files changed since the merge base", ())
ALL_SCOPE = ResolvedScope("all", "--all selects the full backend suite", FULL_SUITE)


@dataclasses.dataclass(frozen=True)
class OtherSuite:
    """A CI test suite that runs outside Bazel, with the commands CI runs for it."""

    name: str
    working_directory: str
    commands: tuple[str, ...]


UI_SUITE = OtherSuite("ui", "cmd/ui", UI_SUITE_COMMANDS)
HELM_SUITE = OtherSuite("helm", ".", HELM_SUITE_COMMANDS)


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
    log_path: str


class BazelLog:
    """Log file for Bazel output, also copied to stderr if stream is set.

    path stays None until the first warm-up or test run creates the file in a new temporary
    directory, so runs without either leave no files behind.
    """

    def __init__(self, stream: bool) -> None:
        self.stream = stream
        self.path: Path | None = None

    def ensure_path(self) -> Path:
        """Returns the log file path, creating its temporary directory on first use."""
        if self.path is None:
            self.path = Path(tempfile.mkdtemp(prefix="run_affected_tests_")) / "bazel.log"
        return self.path


def log_progress(message: str) -> None:
    """Writes a progress line to stderr; stdout is reserved for the JSON result."""
    print(f"[run_affected_tests] {message}", file=sys.stderr, flush=True)


def run_at_repo_root(cmd: Sequence[str], **kwargs: Any) -> subprocess.CompletedProcess[Any]:
    """Runs cmd at the repository root without raising on a non-zero exit code.

    Raises:
        HandledError: If the executable of cmd, e.g. git or bazel, is not installed.
    """
    try:
        return subprocess.run(list(cmd), check=False, cwd=_REPO_ROOT, **kwargs)
    except FileNotFoundError as e:
        raise HandledError(
            f"`{cmd[0]}` is not installed or not on PATH.",
            f"Install {cmd[0]} (install Bazel through Bazelisk, see docs/developer.md) and re-run.",
        ) from e


def run_captured(cmd: Sequence[str]) -> subprocess.CompletedProcess[str]:
    """Runs cmd at the repository root and captures its text output."""
    return run_at_repo_root(cmd, capture_output=True, text=True)


def last_stderr_line(proc: subprocess.CompletedProcess[str]) -> str:
    """Returns the last stderr line of a captured process, which usually names its error."""
    return (proc.stderr.strip().splitlines() or ["no output"])[-1]


def run_git(git_args: Sequence[str]) -> str:
    """Returns the stdout of git run with git_args at the repository root.

    Raises:
        HandledError: If git exits non-zero, e.g. for an unknown base ref.
    """
    proc = run_captured(["git", *git_args])
    if proc.returncode == 0:
        return proc.stdout
    raise HandledError(
        f"`git {shlex.join(git_args)}` failed: {proc.stderr.strip() or proc.returncode}",
        "Run the tool inside the XMC git checkout with a --base-ref that exists locally (run "
        "`git fetch origin` if it is missing, or `git fetch --unshallow` in a shallow clone).",
    )


def collect_changed_files(base_ref: str) -> tuple[str, tuple[str, ...]]:
    """Returns the merge base of base_ref and HEAD and the sorted files changed since.

    Changed files are tracked files that differ from the merge base (committed, staged,
    or unstaged; a rename counts as its old and new path) plus untracked files that are
    not git-ignored.
    """
    merge_base = run_git(["merge-base", base_ref, "HEAD"]).strip()
    tracked = run_git(["diff", "--name-only", "--no-renames", "-z", merge_base]).split("\0")
    untracked = run_git(["ls-files", "--others", "--exclude-standard", "-z"]).split("\0")
    return merge_base, tuple(sorted({path for path in (*tracked, *untracked) if path}))


def run_logged(cmd: Sequence[str], log: BazelLog, keep_stdout: bool = True) -> int:
    """Runs cmd with its output appended to the log file; returns the exit code.

    When streaming, `tee` also copies the output to stderr. With keep_stdout=False only
    stderr is kept, e.g. to drop the label list that the warm-up query prints.
    """
    log_progress(f"Running: {shlex.join(cmd)}")
    log_path = log.ensure_path()
    if not log.stream:
        with log_path.open("ab") as sink:
            log_stdout = sink if keep_stdout else subprocess.DEVNULL
            return run_at_repo_root(cmd, stdout=log_stdout, stderr=sink).returncode
    tee_cmd = ["tee", "-a", str(log_path)]
    with subprocess.Popen(tee_cmd, stdin=subprocess.PIPE, stdout=sys.stderr) as tee:
        tee_stdout = tee.stdin if keep_stdout else subprocess.DEVNULL
        return run_at_repo_root(cmd, stdout=tee_stdout, stderr=tee.stdin).returncode


def parse_scope(resolver_stdout: str) -> ResolvedScope:
    """Parses the JSON scope printed by resolve_test_scope.py."""
    payload = json.loads(resolver_stdout)
    return ResolvedScope(
        backend_mode=payload["backend_mode"],
        reason=payload["reason"],
        bazel_targets=tuple(payload["bazel_targets"].split()),
        run_ui=bool(payload["run_ui"]),
    )


def run_resolver(changed_files: Sequence[str], stream: bool, use_rdeps: bool) -> ResolvedScope:
    """Scopes changed_files with resolve_test_scope.py using pull request semantics.

    The resolver's stderr is copied to stderr if stream is set. Without use_rdeps, the
    resolver runs with --no-rdeps and skips its Bazel query.

    Raises:
        HandledError: If the resolver exits non-zero.
    """
    # Push, dispatch, and schedule events on main force the full suite, so pass the PR
    # event explicitly; an empty --github-output never appends to $GITHUB_OUTPUT.
    cmd = [sys.executable, str(RESOLVER_PATH), "--event", "pull_request", "--github-output", ""]
    rdeps_flags = [] if use_rdeps else ["--no-rdeps"]
    proc = run_captured([*cmd, *rdeps_flags, "--files", *changed_files])
    if stream:
        sys.stderr.write(proc.stderr)
    if proc.returncode == 0:
        return parse_scope(proc.stdout)
    raise HandledError(
        f"{RESOLVER_PATH.name} exited with code {proc.returncode}: {last_stderr_line(proc)}",
        "Re-run with --verbose to see its full output, then fix the error.",
    )


def resolve_scope(
    changed_files: Sequence[str], stream: bool, use_rdeps: bool = True
) -> ResolvedScope:
    """Returns the test scope of changed_files, narrowed by the rdeps query if use_rdeps.

    An empty change set means nothing to run: the resolver would read it as a broken
    CI checkout and pick the full suite, but git failures here raise HandledError.
    """
    if not changed_files:
        return NO_CHANGES_SCOPE
    return run_resolver(changed_files, stream, use_rdeps)


def select_scope(
    changed_files: Sequence[str], log: BazelLog, run_all: bool, warmup: bool
) -> tuple[ResolvedScope, bool]:
    """Returns the test scope to run and whether the warm-up ran.

    run_all selects the full backend suite and runs the resolver without its rdeps query
    only to learn whether CI runs the UI suite. If warmup is set and the resolver's
    offline rdeps query failed, WARMUP_COMMAND fetches the external repositories that
    the query needs and the files are resolved again.
    """
    if run_all:
        run_ui = resolve_scope(changed_files, log.stream, use_rdeps=False).run_ui
        return dataclasses.replace(ALL_SCOPE, run_ui=run_ui), False
    scope = resolve_scope(changed_files, log.stream)
    if not scope.fallback or not warmup:
        return scope, False
    log_progress("The resolver's rdeps query failed; fetching repositories once.")
    if (code := run_logged(WARMUP_COMMAND, log, keep_stdout=False)) != 0:
        log_progress(f"Warning: the warm-up exited with code {code}; see {log.path}.")
    return resolve_scope(changed_files, log.stream), True


def affected_other_suites(changed_files: Sequence[str], run_ui: bool) -> tuple[OtherSuite, ...]:
    """Returns the suites outside Bazel that CI runs for changed_files, UI suite first.

    CI runs the UI suite when the resolver reports run_ui and the Helm suite when a
    changed file matches the paths filter of the run-chart-tests action.
    """
    run_helm = any(
        fnmatch.fnmatchcase(path, pattern)
        for path in changed_files
        for pattern in HELM_SUITE_PATH_PATTERNS
    )
    return tuple(suite for suite, run in ((UI_SUITE, run_ui), (HELM_SUITE, run_helm)) if run)


def query_emulator_dependents(targets: Sequence[str], stream: bool) -> tuple[str, ...] | None:
    """Returns the targets that transitively depend on EMULATOR_PACKAGE, or None on error.

    The query's stderr is copied to stderr if stream is set; a failure logs its last line.
    """
    expr = f"let t = set({' '.join(targets)}) in $t intersect rdeps($t, {EMULATOR_PACKAGE}:all)"
    proc = run_captured(["bazel", "query", "--fetch=false", "--noimplicit_deps", expr])
    if stream:
        sys.stderr.write(proc.stderr)
    if proc.returncode == 0:
        return tuple(line for line in proc.stdout.splitlines() if line.startswith("//"))
    log_progress(f"The emulator dependency query failed: {last_stderr_line(proc)}")
    return None


def targets_need_emulator(scope: ResolvedScope, stream: bool) -> bool:
    """Returns whether any target of scope may need the Spanner emulator.

    The full backend suite always does, and a failed dependency query counts as a
    dependency so that the pre-flight stays conservative.
    """
    if scope.backend_mode == "all":
        return True
    if not scope.bazel_targets:
        return False
    dependents = query_emulator_dependents(scope.bazel_targets, stream)
    if dependents is None:
        log_progress("Assuming the affected tests need the Spanner emulator.")
    return dependents is None or bool(dependents)


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


def is_port_open(host: str, port: int) -> bool:
    """Returns whether a TCP connection to host:port succeeds."""
    try:
        with socket.create_connection((host, port), timeout=EMULATOR_CONNECT_TIMEOUT_SECONDS):
            return True
    except OSError:
        return False


def ensure_emulator_reachable(env: Mapping[str, str]) -> None:
    """Fails fast unless the Spanner emulator at SPANNER_EMULATOR_HOST accepts connections.

    Raises:
        HandledError: If the address is malformed or nothing listens on it.
    """
    address = env.get(EMULATOR_HOST_ENV) or DEFAULT_EMULATOR_HOST
    if is_port_open(*parse_emulator_address(address)):
        return
    raise HandledError(
        f"The affected tests need the Spanner emulator, but nothing listens on {address}.",
        f"Start it with `docker run -d -p 9010:9010 -p 9020:9020 {EMULATOR_IMAGE}` (see "
        "'Running Spanner Database Health Unit Tests' in docs/developer.md), set "
        f"{EMULATOR_HOST_ENV} if it listens elsewhere, or re-run with --skip-emulator-check.",
    )


def build_bazel_test_command(
    targets: Sequence[str], env: Mapping[str, str], bep_path: Path
) -> list[str]:
    """Builds the `bazel test` command line with the CI test flags and a BEP JSON file.

    Bazel runs tests without the client environment, so a set SPANNER_EMULATOR_HOST is
    passed with --test_env; otherwise the tests dial the default address.
    """
    emulator_host = env.get(EMULATOR_HOST_ENV)
    test_env = [f"--test_env={EMULATOR_HOST_ENV}={emulator_host}"] if emulator_host else []
    bep_flag = f"--build_event_json_file={bep_path}"
    return ["bazel", "test", *BAZEL_TEST_FLAGS, *test_env, bep_flag, "--", *targets]


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


def read_test_outcome(bep_path: Path, exit_code: int, log_path: Path) -> BazelTestOutcome:
    """Reads the outcome of a `bazel test` run from its Build Event Protocol JSON file.

    Targets that failed to build get no testSummary event, only an unsuccessful
    targetCompleted event; loading and analysis errors arrive as aborted events.
    """
    statuses: dict[str, str] = {}
    failed_logs: dict[str, list[str]] = {}
    build_errors: list[str] = []
    exit_name = "UNKNOWN"
    lines = bep_path.read_text(encoding="utf-8").splitlines() if bep_path.exists() else []
    for line in filter(str.strip, lines):
        try:
            event = json.loads(line)
        except json.JSONDecodeError:  # e.g. the last line of an interrupted Bazel run
            log_progress(f"Skipped a malformed line in {bep_path}; results may be incomplete.")
            continue
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
    return BazelTestOutcome(
        exit_code, exit_name, passed, failed, not_run, tuple(build_errors), str(log_path)
    )


def run_bazel_tests(
    targets: Sequence[str], env: Mapping[str, str], log: BazelLog
) -> BazelTestOutcome:
    """Runs `bazel test` on targets with its output in the log and reads the results."""
    log_path = log.ensure_path()
    bep_path = log_path.with_name("build_events.json")
    exit_code = run_logged(build_bazel_test_command(targets, env, bep_path), log)
    return read_test_outcome(bep_path, exit_code, log_path)


def raise_for_test_failure(outcome: BazelTestOutcome, report: Mapping[str, Any]) -> None:
    """Raises HandledError with the full report unless `bazel test` succeeded."""
    if outcome.exit_code == 0:
        return
    failed = ", ".join(f"{f.target} ({f.status})" for f in outcome.failed_targets)
    raise HandledError(
        f"bazel test failed with {outcome.exit_name} (exit code {outcome.exit_code}); "
        f"failed targets: {failed or 'none, see build_errors'}; "
        f"targets that did not run: {len(outcome.not_run_targets)}.",
        f"Fix the failures using the test logs in details and the Bazel log {outcome.log_path}, "
        "then re-run python3 ./tools/run_affected_tests.py.",
        details={**report, **dataclasses.asdict(outcome)},
    )


def log_scope(
    changed_files: Sequence[str], scope: ResolvedScope, other_suites: Sequence[OtherSuite]
) -> None:
    """Logs the selected scope, warning about a resolver fallback and unrun other suites."""
    log_progress(f"{len(changed_files)} changed files; {scope.backend_mode}: {scope.reason}.")
    if scope.fallback:
        log_progress(f"Warning: {FALLBACK_EXPLANATION}")
    for suite in other_suites:
        log_progress(
            f"Warning: CI also runs the {suite.name} suite for these changes; run its "
            "commands from other_suites in the JSON result."
        )


def describe_scope(
    base_ref: str, merge_base: str, changed_files: Sequence[str], scope: ResolvedScope
) -> dict[str, Any]:
    """Builds the JSON report fields that describe the selected scope."""
    return {
        "base_ref": base_ref,
        "merge_base": merge_base,
        "changed_files": list(changed_files),
        "backend_mode": scope.backend_mode,
        "reason": scope.reason,
        "fallback": scope.fallback,
        "fallback_reason": FALLBACK_EXPLANATION if scope.fallback else None,
        "bazel_targets": list(scope.bazel_targets),
    }


def run_affected_tests(args: argparse.Namespace, env: Mapping[str, str]) -> dict[str, Any]:
    """Resolves the affected test scope and, unless --dry-run, runs it with Bazel."""
    log = BazelLog(stream=args.verbose or sys.stderr.isatty())
    merge_base, changed_files = collect_changed_files(args.base_ref)
    scope, warmup_ran = select_scope(
        changed_files, log, run_all=args.all, warmup=not args.no_warmup
    )
    other_suites = affected_other_suites(changed_files, scope.run_ui)
    log_scope(changed_files, scope, other_suites)
    emulator_required = targets_need_emulator(scope, log.stream)
    report = describe_scope(args.base_ref, merge_base, changed_files, scope) | {
        "dry_run": args.dry_run,
        "warmup_ran": warmup_ran,
        "emulator_required": emulator_required,
        "other_suites": [dataclasses.asdict(suite) for suite in other_suites],
    }
    if args.dry_run or not scope.bazel_targets:
        # Only the warm-up can have created the log by now.
        return report | {"passed_targets": [], "log_path": str(log.path) if log.path else None}
    if emulator_required and not args.skip_emulator_check:
        ensure_emulator_reachable(env)
    outcome = run_bazel_tests(scope.bazel_targets, env, log)
    raise_for_test_failure(outcome, report)
    return report | {"passed_targets": list(outcome.passed_targets), "log_path": outcome.log_path}


def build_parser() -> argparse.ArgumentParser:
    """Builds the command-line parser."""
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--base-ref",
        default="origin/main",
        help="Ref whose merge base with HEAD is the comparison point (default: origin/main).",
    )
    for flag, help_text in SWITCHES:
        parser.add_argument(flag, action="store_true", help=help_text)
    return parser


def main() -> None:
    """Entry point for the run_affected_tests tool."""
    run_tool(build_parser(), functools.partial(run_affected_tests, env=os.environ))


if __name__ == "__main__":
    main()
