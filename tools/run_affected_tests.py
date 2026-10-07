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
flags. Bazel output goes to a log file (also streamed to stderr with --verbose or when
stderr is a terminal), per-target results come from the Build Event Protocol, and
stdout carries only the JSON result.

Exit codes: 0 = tests passed, dry run, or nothing to run; 1 = handled error (test
failures, git or Bazel problems); 2 = unhandled error.
"""

import argparse
import dataclasses
import json
import shlex
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

# Boolean command-line flags and their help texts.
SWITCHES: tuple[tuple[str, str], ...] = (
    ("--dry-run", "Print the resolved scope without running tests."),
    ("--verbose", "Stream Bazel and resolver output to stderr even if it is not a terminal."),
)


@dataclasses.dataclass(frozen=True)
class ResolvedScope:
    """Backend test scope as reported by resolve_test_scope.py."""

    backend_mode: str
    reason: str
    bazel_targets: tuple[str, ...]


NO_CHANGES_SCOPE = ResolvedScope("none", "no files changed since the merge base", ())


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

    path stays None until the first Bazel command creates the file in a new temporary
    directory, so runs that start no Bazel command leave no files behind.
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


def run_logged(cmd: Sequence[str], log: BazelLog) -> int:
    """Runs cmd with its combined output appended to the log file; returns the exit code.

    When streaming, `tee` also copies the output to stderr.
    """
    log_progress(f"Running: {shlex.join(cmd)}")
    log_path = log.ensure_path()
    if not log.stream:
        with log_path.open("ab") as sink:
            return run_at_repo_root(cmd, stdout=sink, stderr=subprocess.STDOUT).returncode
    tee_cmd = ["tee", "-a", str(log_path)]
    with subprocess.Popen(tee_cmd, stdin=subprocess.PIPE, stdout=sys.stderr) as tee:
        return run_at_repo_root(cmd, stdout=tee.stdin, stderr=subprocess.STDOUT).returncode


def parse_scope(resolver_stdout: str) -> ResolvedScope:
    """Parses the JSON scope printed by resolve_test_scope.py."""
    payload = json.loads(resolver_stdout)
    return ResolvedScope(
        backend_mode=payload["backend_mode"],
        reason=payload["reason"],
        bazel_targets=tuple(payload["bazel_targets"].split()),
    )


def run_resolver(changed_files: Sequence[str], stream: bool) -> ResolvedScope:
    """Scopes changed_files with resolve_test_scope.py using pull request semantics.

    The resolver's stderr is copied to stderr if stream is set.

    Raises:
        HandledError: If the resolver exits non-zero.
    """
    # Push, dispatch, and schedule events on main force the full suite, so pass the PR
    # event explicitly; an empty --github-output never appends to $GITHUB_OUTPUT.
    cmd = [sys.executable, str(RESOLVER_PATH), "--event", "pull_request", "--github-output", ""]
    proc = run_captured([*cmd, "--files", *changed_files])
    if stream:
        sys.stderr.write(proc.stderr)
    if proc.returncode == 0:
        return parse_scope(proc.stdout)
    last_line = (proc.stderr.strip().splitlines() or ["no output"])[-1]
    raise HandledError(
        f"{RESOLVER_PATH.name} exited with code {proc.returncode}: {last_line}",
        "Re-run with --verbose to see its full output, then fix the error.",
    )


def resolve_scope(changed_files: Sequence[str], stream: bool) -> ResolvedScope:
    """Returns the backend test scope of changed_files.

    An empty change set means nothing to run: the resolver would read it as a broken
    CI checkout and pick the full suite, but git failures here raise HandledError.
    """
    if not changed_files:
        return NO_CHANGES_SCOPE
    return run_resolver(changed_files, stream)


def build_bazel_test_command(targets: Sequence[str], bep_path: Path) -> list[str]:
    """Builds the `bazel test` command line with the CI test flags and a BEP JSON file."""
    bep_flag = f"--build_event_json_file={bep_path}"
    return ["bazel", "test", *BAZEL_TEST_FLAGS, bep_flag, "--", *targets]


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


def run_bazel_tests(targets: Sequence[str], log: BazelLog) -> BazelTestOutcome:
    """Runs `bazel test` on targets with its output in the log and reads the results."""
    log_path = log.ensure_path()
    bep_path = log_path.with_name("build_events.json")
    exit_code = run_logged(build_bazel_test_command(targets, bep_path), log)
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
        "bazel_targets": list(scope.bazel_targets),
    }


def run_affected_tests(args: argparse.Namespace) -> dict[str, Any]:
    """Resolves the affected test scope and, unless --dry-run, runs it with Bazel."""
    log = BazelLog(stream=args.verbose or sys.stderr.isatty())
    merge_base, changed_files = collect_changed_files(args.base_ref)
    scope = resolve_scope(changed_files, log.stream)
    log_progress(f"{len(changed_files)} changed files; {scope.backend_mode}: {scope.reason}.")
    report = describe_scope(args.base_ref, merge_base, changed_files, scope) | {
        "dry_run": args.dry_run
    }
    if args.dry_run or not scope.bazel_targets:
        return report | {"passed_targets": [], "log_path": None}
    outcome = run_bazel_tests(scope.bazel_targets, log)
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
    run_tool(build_parser(), run_affected_tests)


if __name__ == "__main__":
    main()
