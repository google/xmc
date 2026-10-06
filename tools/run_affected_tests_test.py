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

"""Unit tests for the run_affected_tests tool."""

import argparse
import contextlib
import dataclasses
import io
import json
import subprocess
import sys
import tempfile
import unittest
from collections.abc import Sequence
from pathlib import Path
from typing import Any
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import run_affected_tests  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402

ResolvedScope = run_affected_tests.ResolvedScope
FailedTarget = run_affected_tests.FailedTarget
BazelTestOutcome = run_affected_tests.BazelTestOutcome

CHANGED_FILES = ("cmd/x.go", "docs/a.md")
RDEPS_SCOPE = ResolvedScope(
    "rdeps", "discovered 2 affected bazel test targets", ("//tools:a_test", "//internal/x:x_test")
)
NONE_SCOPE = ResolvedScope("none", "only non-code or documentation files modified", ())
PASSING_OUTCOME = BazelTestOutcome(
    exit_code=0,
    exit_name="SUCCESS",
    passed_targets=("//internal/x:x_test", "//tools:a_test"),
    failed_targets=(),
    not_run_targets=(),
    build_errors=(),
    log_path="/tmp/bazel.log",
)
FAILING_OUTCOME = dataclasses.replace(
    PASSING_OUTCOME,
    exit_code=3,
    exit_name="TESTS_FAILED",
    passed_targets=("//tools:a_test",),
    failed_targets=(FailedTarget("//internal/x:x_test", "FAILED", ("/logs/x/test.log",)),),
)
BUILD_FAILURE_OUTCOME = dataclasses.replace(
    FAILING_OUTCOME,
    exit_code=1,
    exit_name="BUILD_FAILURE",
    failed_targets=(),
    not_run_targets=("//internal/x:x_test",),
    build_errors=("no such target '//x:y_test'",),
)
MIXED_STATUSES = {
    "//c:t": "NO_STATUS",
    "//b:t": "TIMEOUT",
    "//a:t": "PASSED",
    "//e:t": "TOOL_HALTED_BEFORE_TESTING",
    "//d:t": "FLAKY",
}
BAZEL_TEST_CMD = ("bazel", "test", "//a:a_test")
PRINT_TO_BOTH_STREAMS = "import sys; print('to-stdout'); print('to-stderr', file=sys.stderr)"


def completed(
    stdout: str = "", returncode: int = 0, stderr: str = ""
) -> subprocess.CompletedProcess[str]:
    """Builds a finished text-mode subprocess result."""
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def resolver_json(scope: ResolvedScope) -> str:
    """Renders a scope the way resolve_test_scope.py prints it."""
    targets = " ".join(scope.bazel_targets)
    fields = {"run_backend": bool(targets), "run_ui": False, "bazel_targets": targets}
    return json.dumps(fields | {"backend_mode": scope.backend_mode, "reason": scope.reason})


def make_args(**overrides: Any) -> argparse.Namespace:
    """Builds the parsed CLI defaults, applying overrides."""
    defaults = vars(run_affected_tests.build_parser().parse_args([]))
    return argparse.Namespace(**(defaults | overrides))


def summary_event(label: str, status: str, failed_logs: tuple[str, ...] = ()) -> dict[str, Any]:
    """Builds a BEP testSummary event."""
    failed = [{"uri": f"file://{path}"} for path in failed_logs]
    summary = {"overallStatus": status, "failed": failed}
    return {"id": {"testSummary": {"label": label}}, "testSummary": summary}


def target_completed(label: str, success: bool) -> dict[str, Any]:
    """Builds a BEP targetCompleted event; Bazel omits success when it is false."""
    result: dict[str, Any] = {"success": True} if success else {"failureDetail": {"message": "x"}}
    return {"id": {"targetCompleted": {"label": label}}, "completed": result}


def build_finished(exit_name: str) -> dict[str, Any]:
    """Builds a BEP buildFinished event."""
    return {"id": {"buildFinished": {}}, "finished": {"exitCode": {"name": exit_name}}}


class TempLogTestCase(unittest.TestCase):
    """Base class with a quiet BazelLog that writes to a temporary directory and no progress logs."""

    def setUp(self) -> None:
        self.temp_dir = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.log_path = self.temp_dir / "bazel.log"
        temp_dir_patch = mock.patch.object(tempfile, "mkdtemp", return_value=str(self.temp_dir))
        self.mkdtemp = self.enterContext(temp_dir_patch)
        self.log = run_affected_tests.BazelLog(stream=False)
        self.enterContext(mock.patch.object(run_affected_tests, "log_progress"))

    def write_events(self, *events: dict[str, Any]) -> Path:
        """Writes Build Event Protocol JSON events, one per line, and returns the file."""
        bep_path = self.temp_dir / "build_events.json"
        bep_path.write_text("".join(json.dumps(e) + "\n" for e in events), encoding="utf-8")
        return bep_path


class CollectChangedFilesTest(unittest.TestCase):
    """Tests for changed-file collection from git."""

    def patch_git_outputs(self, *outputs: subprocess.CompletedProcess[str]) -> Any:
        """Patches run_captured to return outputs in order."""
        patcher = mock.patch.object(run_affected_tests, "run_captured", side_effect=outputs)
        return self.enterContext(patcher)

    def collect_from_git(self) -> tuple[str, tuple[str, ...]]:
        """Collects changed files with merge-base, diff, and ls-files outputs patched."""
        self.patch_git_outputs(
            completed("abc123\n"), completed("b.go\0a.py\0"), completed("c.md\0a.py\0")
        )
        return run_affected_tests.collect_changed_files("origin/main")

    def test_returns_the_merge_base(self) -> None:
        merge_base, _ = self.collect_from_git()
        self.assertEqual(merge_base, "abc123")

    def test_returns_sorted_unique_tracked_and_untracked_files(self) -> None:
        _, files = self.collect_from_git()
        self.assertEqual(files, ("a.py", "b.go", "c.md"))

    def test_diff_counts_renames_as_old_and_new_path(self) -> None:
        run = self.patch_git_outputs(completed("abc123\n"), completed(""), completed(""))
        run_affected_tests.collect_changed_files("origin/main")
        diff_cmd = run.call_args_list[1].args[0]
        self.assertEqual(diff_cmd, ["git", "diff", "--name-only", "--no-renames", "-z", "abc123"])

    def test_git_failure_message_includes_git_stderr(self) -> None:
        self.patch_git_outputs(completed(returncode=128, stderr="fatal: Not a valid object name"))
        with self.assertRaises(HandledError) as ctx:
            run_affected_tests.collect_changed_files("origin/nope")
        self.assertIn("Not a valid object name", ctx.exception.message)

    def test_git_failure_instruction_covers_shallow_clones(self) -> None:
        self.patch_git_outputs(completed(returncode=128, stderr="fatal: no merge base"))
        with self.assertRaises(HandledError) as ctx:
            run_affected_tests.collect_changed_files("origin/main")
        self.assertIn("git fetch --unshallow", ctx.exception.instruction)

    def test_missing_git_raises_handled_error(self) -> None:
        with (
            mock.patch.object(subprocess, "run", side_effect=FileNotFoundError("git")),
            self.assertRaises(HandledError) as ctx,
        ):
            run_affected_tests.collect_changed_files("origin/main")
        self.assertIn("`git` is not installed", ctx.exception.message)


class BazelLogTest(TempLogTestCase):
    """Tests for creating the log file lazily and running commands into it."""

    def run_quietly(self, output: bytes = b"", returncode: int = 0) -> tuple[int, Any]:
        """Runs BAZEL_TEST_CMD with a fake printing output; returns the exit code and the fake."""

        def fake_run(cmd: Sequence[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
            kwargs["stdout"].write(output)
            return completed(returncode=returncode)

        with mock.patch.object(run_affected_tests, "run_at_repo_root", side_effect=fake_run) as run:
            return run_affected_tests.run_logged(BAZEL_TEST_CMD, self.log), run

    def run_streaming(self) -> tuple[Any, Any]:
        """Runs BAZEL_TEST_CMD while streaming; returns the Popen and run_at_repo_root mocks."""
        log, proc = run_affected_tests.BazelLog(stream=True), completed()
        with (
            mock.patch.object(subprocess, "Popen") as popen,
            mock.patch.object(run_affected_tests, "run_at_repo_root", return_value=proc) as run,
        ):
            run_affected_tests.run_logged(BAZEL_TEST_CMD, log)
        return popen, run

    def test_ensure_path_creates_one_directory_per_log(self) -> None:
        self.log.ensure_path()
        self.log.ensure_path()
        self.mkdtemp.assert_called_once_with(prefix="run_affected_tests_")

    def test_quiet_runs_append_their_output_to_the_log_file(self) -> None:
        self.run_quietly(b"first\n")
        self.run_quietly(b"second\n")
        self.assertEqual(self.log_path.read_bytes(), b"first\nsecond\n")

    def test_quiet_run_merges_stderr_into_the_log(self) -> None:
        _, run = self.run_quietly()
        self.assertIs(run.call_args.kwargs["stderr"], subprocess.STDOUT)

    def test_run_returns_the_exit_code(self) -> None:
        exit_code, _ = self.run_quietly(returncode=3)
        self.assertEqual(exit_code, 3)

    def test_streaming_run_tees_into_the_log_file_and_stderr(self) -> None:
        popen, _ = self.run_streaming()
        tee_cmd = ["tee", "-a", str(self.log_path)]
        popen.assert_called_once_with(tee_cmd, stdin=subprocess.PIPE, stdout=sys.stderr)

    def test_streaming_run_pipes_combined_output_into_tee(self) -> None:
        popen, run = self.run_streaming()
        tee_stdin = popen.return_value.__enter__.return_value.stdin
        run.assert_called_once_with(BAZEL_TEST_CMD, stdout=tee_stdin, stderr=subprocess.STDOUT)

    def test_smoke_streaming_run_with_real_processes(self) -> None:
        """Smoke test, the only one spawning processes: real python and tee fill log and stderr."""
        log = run_affected_tests.BazelLog(stream=True)
        with tempfile.TemporaryFile("w+", encoding="utf-8") as stderr:
            with mock.patch.object(sys, "stderr", stderr):
                run_affected_tests.run_logged([sys.executable, "-c", PRINT_TO_BOTH_STREAMS], log)
            stderr.seek(0)
            streamed = sorted(stderr.read().split())
        logged = sorted(self.log_path.read_text(encoding="utf-8").split())
        self.assertEqual([streamed, logged], [["to-stderr", "to-stdout"]] * 2)

    def test_missing_executable_raises_handled_error(self) -> None:
        with (
            mock.patch.object(subprocess, "run", side_effect=FileNotFoundError("bazel")),
            self.assertRaises(HandledError) as ctx,
        ):
            run_affected_tests.run_logged(BAZEL_TEST_CMD, self.log)
        self.assertIn("`bazel` is not installed", ctx.exception.message)


class ResolveScopeTest(unittest.TestCase):
    """Tests for scoping changed files with resolve_test_scope.py."""

    def resolve_with(
        self, proc: subprocess.CompletedProcess[str], stream: bool = False
    ) -> tuple[ResolvedScope, Any]:
        """Resolves a change set with run_captured returning proc; returns the scope and mock."""
        with mock.patch.object(run_affected_tests, "run_captured", return_value=proc) as run:
            return run_affected_tests.resolve_scope(["tools/a.py"], stream), run

    def stderr_of_resolution(self, stream: bool) -> str:
        """Returns what resolving writes to stderr when the resolver prints a DEBUG line."""
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            self.resolve_with(completed(resolver_json(RDEPS_SCOPE), stderr="DEBUG x\n"), stream)
        return stderr.getvalue()

    def test_invokes_resolver_with_pull_request_event_and_files(self) -> None:
        _, run = self.resolve_with(completed(resolver_json(RDEPS_SCOPE)))
        resolver = [sys.executable, str(run_affected_tests.RESOLVER_PATH)]
        resolver_args = ["--event", "pull_request", "--github-output", "", "--files", "tools/a.py"]
        self.assertEqual(run.call_args.args[0], [*resolver, *resolver_args])

    def test_parses_the_resolved_scope(self) -> None:
        scope, _ = self.resolve_with(completed(resolver_json(RDEPS_SCOPE)))
        self.assertEqual(scope, RDEPS_SCOPE)

    def test_streaming_copies_resolver_stderr_to_stderr(self) -> None:
        self.assertEqual(self.stderr_of_resolution(stream=True), "DEBUG x\n")

    def test_quiet_resolution_keeps_resolver_stderr_off_stderr(self) -> None:
        self.assertEqual(self.stderr_of_resolution(stream=False), "")

    def test_resolver_crash_message_has_last_stderr_line(self) -> None:
        with self.assertRaises(HandledError) as ctx:
            self.resolve_with(completed(returncode=1, stderr="Traceback\nKeyError: 'x'\n"))
        self.assertIn("exited with code 1: KeyError: 'x'", ctx.exception.message)

    def test_resolver_crash_instruction_suggests_verbose(self) -> None:
        with self.assertRaises(HandledError) as ctx:
            self.resolve_with(completed(returncode=1, stderr="Traceback\n"))
        self.assertIn("--verbose", ctx.exception.instruction)

    def test_empty_change_set_means_nothing_to_run(self) -> None:
        scope = run_affected_tests.resolve_scope([], stream=False)
        self.assertEqual(scope, run_affected_tests.NO_CHANGES_SCOPE)

    def test_empty_change_set_skips_resolver(self) -> None:
        with mock.patch.object(run_affected_tests, "run_captured") as run:
            run_affected_tests.resolve_scope([], stream=False)
        run.assert_not_called()


class BuildBazelTestCommandTest(unittest.TestCase):
    """Tests for the `bazel test` command line."""

    def test_includes_ci_flags_bep_file_and_targets(self) -> None:
        cmd = run_affected_tests.build_bazel_test_command(["//a:a_test"], Path("/tmp/bep.json"))
        ci_flags = ["--test_output=errors", "--experimental_ui_max_stdouterr_bytes=2097152"]
        ci_flags += ["--build_tests_only", "--test_timeout=60,300,300,300"]
        bep_flag = "--build_event_json_file=/tmp/bep.json"
        self.assertEqual(cmd, ["bazel", "test", *ci_flags, bep_flag, "--", "//a:a_test"])


class ReadTestOutcomeTest(TempLogTestCase):
    """Tests for reading per-target results from Build Event Protocol files."""

    def read(self, *events: dict[str, Any], exit_code: int = 0) -> BazelTestOutcome:
        """Reads the outcome of a BEP file holding events."""
        bep_path = self.write_events(*events)
        return run_affected_tests.read_test_outcome(bep_path, exit_code, self.log_path)

    def test_passed_and_flaky_targets_pass(self) -> None:
        flaky, passed = summary_event("//b:b_test", "FLAKY"), summary_event("//a:a_test", "PASSED")
        self.assertEqual(self.read(flaky, passed).passed_targets, ("//a:a_test", "//b:b_test"))

    def test_failed_test_reports_status_and_test_log(self) -> None:
        failed = summary_event("//a:a_test", "FAILED", ("/logs/a/test.log",))
        expected = FailedTarget("//a:a_test", "FAILED", ("/logs/a/test.log",))
        self.assertEqual(self.read(failed, exit_code=3).failed_targets, (expected,))

    def test_test_that_never_ran_is_reported_as_not_run(self) -> None:
        outcome = self.read(summary_event("//a:a_test", "NO_STATUS"), exit_code=1)
        self.assertEqual(outcome.not_run_targets, ("//a:a_test",))

    def test_target_that_failed_to_build_is_reported(self) -> None:
        outcome = self.read(target_completed("//a:a_test", success=False), exit_code=1)
        self.assertEqual(outcome.failed_targets, (FailedTarget("//a:a_test", "FAILED_TO_BUILD"),))

    def test_built_target_is_not_reported_as_failed(self) -> None:
        outcome = self.read(target_completed("//a:a_test", success=True))
        self.assertEqual(outcome.failed_targets, ())

    def test_exit_name_comes_from_build_finished_event(self) -> None:
        outcome = self.read(build_finished("BUILD_FAILURE"), exit_code=1)
        self.assertEqual(outcome.exit_name, "BUILD_FAILURE")

    def test_loading_failure_is_a_build_error(self) -> None:
        aborted = {"reason": "LOADING_FAILURE", "description": "no such target"}
        outcome = self.read({"id": {"pattern": {}}, "aborted": aborted}, exit_code=1)
        self.assertEqual(outcome.build_errors, ("no such target",))

    def test_missing_event_file_yields_no_results(self) -> None:
        missing = self.temp_dir / "missing.json"
        outcome = run_affected_tests.read_test_outcome(missing, 37, Path("/l"))
        self.assertEqual(outcome, BazelTestOutcome(37, "UNKNOWN", (), (), (), (), "/l"))


class RunBazelTestsTest(TempLogTestCase):
    """Tests for running `bazel test` into the log and reading its results."""

    def test_writes_build_events_next_to_the_log_file(self) -> None:
        with mock.patch.object(run_affected_tests, "run_logged", return_value=0) as run:
            run_affected_tests.run_bazel_tests(["//a:a_test"], self.log)
        bep_flag = f"--build_event_json_file={self.temp_dir / 'build_events.json'}"
        self.assertIn(bep_flag, run.call_args.args[0])


class SummarizeStatusesTest(unittest.TestCase):
    """Tests for splitting test statuses into passed, failed, and never-run targets."""

    def test_passed_and_flaky_targets_are_passed_in_order(self) -> None:
        passed, _, _ = run_affected_tests.summarize_statuses(MIXED_STATUSES, {})
        self.assertEqual(passed, ("//a:t", "//d:t"))

    def test_other_statuses_are_failed_with_their_logs(self) -> None:
        _, failed, _ = run_affected_tests.summarize_statuses(MIXED_STATUSES, {"//b:t": ["/b.log"]})
        self.assertEqual(failed, (FailedTarget("//b:t", "TIMEOUT", ("/b.log",)),))

    def test_targets_that_never_ran_are_not_run_in_order(self) -> None:
        _, _, not_run = run_affected_tests.summarize_statuses(MIXED_STATUSES, {})
        self.assertEqual(not_run, ("//c:t", "//e:t"))


class RaiseForTestFailureTest(unittest.TestCase):
    """Tests for turning failed `bazel test` runs into handled errors."""

    def raise_for(self, outcome: BazelTestOutcome) -> HandledError:
        """Returns the HandledError raised for outcome with a one-field report."""
        with self.assertRaises(HandledError) as ctx:
            run_affected_tests.raise_for_test_failure(outcome, {"base_ref": "origin/main"})
        return ctx.exception

    def failure_details(self, outcome: BazelTestOutcome) -> Any:
        """Returns the details raised for outcome after a JSON round trip, as run_tool emits."""
        return json.loads(json.dumps(self.raise_for(outcome).details))

    def test_success_does_not_raise(self) -> None:
        run_affected_tests.raise_for_test_failure(PASSING_OUTCOME, {})

    def test_failure_message_names_exit_status(self) -> None:
        self.assertIn("TESTS_FAILED (exit code 3)", self.raise_for(FAILING_OUTCOME).message)

    def test_failure_message_names_failed_targets(self) -> None:
        self.assertIn("//internal/x:x_test (FAILED)", self.raise_for(FAILING_OUTCOME).message)

    def test_build_failure_message_points_to_build_errors(self) -> None:
        self.assertIn("see build_errors", self.raise_for(BUILD_FAILURE_OUTCOME).message)

    def test_failure_message_counts_targets_that_did_not_run(self) -> None:
        self.assertIn("targets that did not run: 1.", self.raise_for(BUILD_FAILURE_OUTCOME).message)

    def test_failure_details_include_the_report(self) -> None:
        self.assertEqual(self.failure_details(FAILING_OUTCOME)["base_ref"], "origin/main")

    def test_failure_details_include_the_bazel_log(self) -> None:
        self.assertEqual(self.failure_details(FAILING_OUTCOME)["log_path"], "/tmp/bazel.log")

    def test_failure_details_include_build_errors(self) -> None:
        build_errors = self.failure_details(BUILD_FAILURE_OUTCOME)["build_errors"]
        self.assertEqual(build_errors, ["no such target '//x:y_test'"])

    def test_failure_details_list_targets_that_did_not_run(self) -> None:
        not_run = self.failure_details(BUILD_FAILURE_OUTCOME)["not_run_targets"]
        self.assertEqual(not_run, ["//internal/x:x_test"])

    def test_failure_details_serialize_failed_targets(self) -> None:
        failed_targets = self.failure_details(FAILING_OUTCOME)["failed_targets"]
        failure = {"target": "//internal/x:x_test", "status": "FAILED"}
        self.assertEqual(failed_targets, [failure | {"test_logs": ["/logs/x/test.log"]}])


class RunAffectedTestsTest(TempLogTestCase):
    """Tests for the end-to-end orchestration with git, the resolver, and Bazel mocked."""

    def setUp(self) -> None:
        super().setUp()
        self.enterContext(contextlib.redirect_stderr(io.StringIO()))
        self.enterContext(
            mock.patch.object(
                run_affected_tests, "collect_changed_files", return_value=("abc123", CHANGED_FILES)
            )
        )
        self.bazel = self.enterContext(
            mock.patch.object(run_affected_tests, "run_bazel_tests", return_value=PASSING_OUTCOME)
        )

    def run_affected(self, scope: ResolvedScope, **args: Any) -> dict[str, Any]:
        """Runs run_affected_tests on scope with the given CLI overrides."""
        with mock.patch.object(run_affected_tests, "resolve_scope", return_value=scope):
            return run_affected_tests.run_affected_tests(make_args(**args))

    def test_dry_run_skips_bazel(self) -> None:
        self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.bazel.assert_not_called()

    def test_dry_run_reports_the_resolved_targets(self) -> None:
        result = self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.assertEqual(result["bazel_targets"], list(RDEPS_SCOPE.bazel_targets))

    def test_dry_run_is_reported(self) -> None:
        self.assertTrue(self.run_affected(RDEPS_SCOPE, dry_run=True)["dry_run"])

    def test_dry_run_reports_no_log_when_nothing_was_logged(self) -> None:
        self.assertIsNone(self.run_affected(RDEPS_SCOPE, dry_run=True)["log_path"])

    def test_dry_run_creates_no_temporary_directory(self) -> None:
        self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.mkdtemp.assert_not_called()

    def test_reports_the_changed_files(self) -> None:
        result = self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.assertEqual(result["changed_files"], list(CHANGED_FILES))

    def test_verbose_streams_resolver_output(self) -> None:
        with mock.patch.object(run_affected_tests, "resolve_scope", return_value=NONE_SCOPE) as res:
            run_affected_tests.run_affected_tests(make_args(verbose=True))
        res.assert_called_once_with(CHANGED_FILES, True)

    def test_nothing_to_run_skips_bazel(self) -> None:
        self.run_affected(NONE_SCOPE)
        self.bazel.assert_not_called()

    def test_nothing_to_run_reports_no_passed_targets(self) -> None:
        self.assertEqual(self.run_affected(NONE_SCOPE)["passed_targets"], [])

    def test_runs_bazel_on_the_resolved_targets(self) -> None:
        self.run_affected(RDEPS_SCOPE)
        self.bazel.assert_called_once_with(RDEPS_SCOPE.bazel_targets, mock.ANY)

    def test_output_is_quiet_when_stderr_is_not_a_terminal(self) -> None:
        self.run_affected(RDEPS_SCOPE)
        self.assertFalse(self.bazel.call_args.args[1].stream)

    def test_verbose_streams_bazel_output(self) -> None:
        self.run_affected(RDEPS_SCOPE, verbose=True)
        self.assertTrue(self.bazel.call_args.args[1].stream)

    def test_passing_run_reports_passed_targets(self) -> None:
        result = self.run_affected(RDEPS_SCOPE)
        self.assertEqual(result["passed_targets"], list(PASSING_OUTCOME.passed_targets))

    def test_passing_run_reports_the_bazel_log(self) -> None:
        self.assertEqual(self.run_affected(RDEPS_SCOPE)["log_path"], PASSING_OUTCOME.log_path)

    def test_success_payload_has_no_failed_targets(self) -> None:
        self.assertNotIn("failed_targets", self.run_affected(RDEPS_SCOPE))

    def test_failing_run_raises_with_report_and_outcome_details(self) -> None:
        self.bazel.return_value = FAILING_OUTCOME
        with self.assertRaises(HandledError) as ctx:
            self.run_affected(RDEPS_SCOPE)
        details = ctx.exception.details or {}
        self.assertEqual((details["merge_base"], details["exit_name"]), ("abc123", "TESTS_FAILED"))


class BuildParserTest(unittest.TestCase):
    """Tests for the command-line interface."""

    def test_defaults(self) -> None:
        expected = {"base_ref": "origin/main", "dry_run": False, "verbose": False}
        self.assertEqual(vars(run_affected_tests.build_parser().parse_args([])), expected)


class MainTest(unittest.TestCase):
    """Tests for the JSON output and exit codes of main()."""

    def run_main(self, side_effect: Any) -> tuple[object, str, str]:
        """Runs main() with run_affected_tests replaced; returns the exit code and output."""
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            mock.patch.object(sys, "argv", ["run_affected_tests.py", "--dry-run"]),
            mock.patch.object(run_affected_tests, "run_affected_tests", side_effect=side_effect),
            contextlib.redirect_stdout(stdout),
            contextlib.redirect_stderr(stderr),
            self.assertRaises(SystemExit) as ctx,
        ):
            run_affected_tests.main()
        return ctx.exception.code, stdout.getvalue(), stderr.getvalue()

    def test_success_prints_json_result_and_exits_zero(self) -> None:
        code, stdout, _ = self.run_main(lambda args: {"dry_run": args.dry_run})
        self.assertEqual((code, json.loads(stdout)["result"]), (0, {"dry_run": True}))

    def test_handled_error_prints_details_and_exits_one(self) -> None:
        error = HandledError("tests failed", "fix them", {"log_path": "/l"})
        code, _, stderr = self.run_main(error)
        self.assertEqual((code, json.loads(stderr)["details"]), (1, {"log_path": "/l"}))

    def test_unexpected_error_exits_two(self) -> None:
        code, _, stderr = self.run_main(RuntimeError("boom"))
        self.assertEqual((code, json.loads(stderr)["error_type"]), (2, "unhandled"))


if __name__ == "__main__":
    unittest.main()
