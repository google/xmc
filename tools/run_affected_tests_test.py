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
import os
import socket
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
from tools.benchmark import resolve_test_scope  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402

ResolvedScope = run_affected_tests.ResolvedScope
FailedTarget = run_affected_tests.FailedTarget
BazelTestOutcome = run_affected_tests.BazelTestOutcome

CHANGED_FILES = ("cmd/x.go", "docs/a.md")
FULL_SUITE = ("//...", "-//cmd/ui/...")
RDEPS_SCOPE = ResolvedScope(
    "rdeps", "discovered 2 affected bazel test targets", ("//tools:a_test", "//internal/x:x_test")
)
NONE_SCOPE = ResolvedScope("none", "only non-code or documentation files modified", ())
FALLBACK_SCOPE = ResolvedScope("all", resolve_test_scope.RDEPS_QUERY_FAILED_REASON, FULL_SUITE)
NO_TARGETS_SCOPE = ResolvedScope("all", resolve_test_scope.NO_RDEPS_TARGETS_REASON, FULL_SUITE)
CORE_INFRA_SCOPE = ResolvedScope("all", "core infra modified or rdeps disabled", FULL_SUITE)
EMULATOR_DEPENDENTS = ("//internal/x:x_test",)
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
MALFORMED_EMULATOR_HOSTS = (
    "localhost",
    ":9010",
    "localhost:",
    "localhost:grpc",
    "localhost:0",
    "localhost:65536",
    "localhost:\N{SUPERSCRIPT TWO}",
)


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
        self.progress = self.enterContext(mock.patch.object(run_affected_tests, "log_progress"))

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

    def run_quietly(
        self, output: bytes = b"", returncode: int = 0, keep_stdout: bool = True
    ) -> tuple[int, Any]:
        """Runs BAZEL_TEST_CMD with a fake printing output; returns the exit code and the fake."""

        def fake_run(cmd: Sequence[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
            kwargs["stderr"].write(output)
            return completed(returncode=returncode)

        with mock.patch.object(run_affected_tests, "run_at_repo_root", side_effect=fake_run) as run:
            return run_affected_tests.run_logged(BAZEL_TEST_CMD, self.log, keep_stdout), run

    def run_streaming(self, keep_stdout: bool = True) -> tuple[Any, Any]:
        """Runs BAZEL_TEST_CMD while streaming; returns the Popen and run_at_repo_root mocks."""
        log, proc = run_affected_tests.BazelLog(stream=True), completed()
        with (
            mock.patch.object(subprocess, "Popen") as popen,
            mock.patch.object(run_affected_tests, "run_at_repo_root", return_value=proc) as run,
        ):
            run_affected_tests.run_logged(BAZEL_TEST_CMD, log, keep_stdout)
        return popen, run

    def test_ensure_path_creates_one_directory_per_log(self) -> None:
        self.log.ensure_path()
        self.log.ensure_path()
        self.mkdtemp.assert_called_once_with(prefix="run_affected_tests_")

    def test_quiet_runs_append_their_output_to_the_log_file(self) -> None:
        self.run_quietly(b"first\n")
        self.run_quietly(b"second\n")
        self.assertEqual(self.log_path.read_bytes(), b"first\nsecond\n")

    def test_quiet_run_logs_stdout_and_stderr(self) -> None:
        _, run = self.run_quietly()
        self.assertIs(run.call_args.kwargs["stderr"], run.call_args.kwargs["stdout"])

    def test_quiet_run_without_stdout_logs_only_stderr(self) -> None:
        _, run = self.run_quietly(keep_stdout=False)
        stdout, stderr = run.call_args.kwargs["stdout"], run.call_args.kwargs["stderr"]
        self.assertEqual((stdout, stderr.name), (subprocess.DEVNULL, str(self.log_path)))

    def test_run_returns_the_exit_code(self) -> None:
        exit_code, _ = self.run_quietly(returncode=3)
        self.assertEqual(exit_code, 3)

    def test_streaming_run_tees_into_the_log_file_and_stderr(self) -> None:
        popen, _ = self.run_streaming()
        tee_cmd = ["tee", "-a", str(self.log_path)]
        popen.assert_called_once_with(tee_cmd, stdin=subprocess.PIPE, stdout=sys.stderr)

    def test_streaming_run_pipes_stdout_and_stderr_into_tee(self) -> None:
        popen, run = self.run_streaming()
        tee_stdin = popen.return_value.__enter__.return_value.stdin
        run.assert_called_once_with(BAZEL_TEST_CMD, stdout=tee_stdin, stderr=tee_stdin)

    def test_streaming_run_without_stdout_pipes_only_stderr_into_tee(self) -> None:
        popen, run = self.run_streaming(keep_stdout=False)
        tee_stdin = popen.return_value.__enter__.return_value.stdin
        run.assert_called_once_with(BAZEL_TEST_CMD, stdout=subprocess.DEVNULL, stderr=tee_stdin)

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


class ResolvedScopeTest(unittest.TestCase):
    """Tests for telling a failed rdeps query apart from intentional full-suite runs."""

    def test_failed_rdeps_query_is_a_fallback(self) -> None:
        self.assertTrue(FALLBACK_SCOPE.fallback)

    def test_narrowed_scope_is_not_a_fallback(self) -> None:
        self.assertFalse(RDEPS_SCOPE.fallback)

    def test_core_infra_full_suite_is_not_a_fallback(self) -> None:
        self.assertFalse(CORE_INFRA_SCOPE.fallback)

    def test_full_suite_without_rdeps_targets_is_not_a_fallback(self) -> None:
        self.assertFalse(NO_TARGETS_SCOPE.fallback)


class ResolverContractTest(unittest.TestCase):
    """Tests that pin the resolve_test_scope.py outputs this tool relies on."""

    def resolve_with_rdeps(self, rdeps_targets: list[str] | None) -> ResolvedScope:
        """Resolves a Go change with the resolver's rdeps query returning rdeps_targets."""
        with mock.patch.object(
            resolve_test_scope, "discover_bazel_rdeps_targets", return_value=rdeps_targets
        ):
            result = resolve_test_scope.resolve_test_scope(["internal/x/x.go"])
        return run_affected_tests.parse_scope(json.dumps(result))

    def test_failed_rdeps_query_is_a_fallback(self) -> None:
        self.assertTrue(self.resolve_with_rdeps(None).fallback)

    def test_rdeps_query_without_targets_is_not_a_fallback(self) -> None:
        self.assertFalse(self.resolve_with_rdeps([]).fallback)

    def test_all_scope_matches_the_resolver_full_suite(self) -> None:
        result = resolve_test_scope.resolve_test_scope(["MODULE.bazel"])
        targets = tuple(str(result["bazel_targets"]).split())
        self.assertEqual(targets, run_affected_tests.ALL_SCOPE.bazel_targets)


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


class SelectScopeTest(TempLogTestCase):
    """Tests for --all, the resolver, and the warm-up after a failed rdeps query."""

    def setUp(self) -> None:
        super().setUp()
        self.resolve = self.enterContext(mock.patch.object(run_affected_tests, "resolve_scope"))
        self.warmup = self.enterContext(
            mock.patch.object(run_affected_tests, "run_logged", return_value=0)
        )

    def select(
        self, *scopes: ResolvedScope, run_all: bool = False, warmup: bool = True
    ) -> tuple[ResolvedScope, bool]:
        """Selects a scope with resolve_scope returning scopes in order."""
        self.resolve.side_effect = scopes
        return run_affected_tests.select_scope(CHANGED_FILES, self.log, run_all, warmup)

    def test_all_selects_the_full_suite(self) -> None:
        self.assertEqual(self.select(run_all=True), (run_affected_tests.ALL_SCOPE, False))

    def test_all_skips_the_resolver(self) -> None:
        self.select(run_all=True)
        self.resolve.assert_not_called()

    def test_narrowed_scope_needs_no_warmup(self) -> None:
        self.assertEqual(self.select(RDEPS_SCOPE), (RDEPS_SCOPE, False))

    def test_full_suite_without_rdeps_targets_skips_the_warmup(self) -> None:
        self.select(NO_TARGETS_SCOPE)
        self.warmup.assert_not_called()

    def test_fallback_runs_the_warmup_without_its_stdout(self) -> None:
        self.select(FALLBACK_SCOPE, RDEPS_SCOPE)
        warmup = mock.call(run_affected_tests.WARMUP_COMMAND, self.log, keep_stdout=False)
        self.assertEqual(self.warmup.call_args_list, [warmup])

    def test_fallback_resolves_again_after_the_warmup(self) -> None:
        self.assertEqual(self.select(FALLBACK_SCOPE, RDEPS_SCOPE), (RDEPS_SCOPE, True))

    def test_failed_warmup_logs_a_warning(self) -> None:
        self.warmup.return_value = 3
        self.select(FALLBACK_SCOPE, RDEPS_SCOPE)
        self.assertIn("warm-up exited with code 3", self.progress.call_args.args[0])

    def test_no_warmup_keeps_the_fallback(self) -> None:
        self.assertEqual(self.select(FALLBACK_SCOPE, warmup=False), (FALLBACK_SCOPE, False))

    def test_no_warmup_skips_the_warmup(self) -> None:
        self.select(FALLBACK_SCOPE, warmup=False)
        self.warmup.assert_not_called()


class QueryEmulatorDependentsTest(unittest.TestCase):
    """Tests for the Bazel query that finds targets depending on the Spanner emulator."""

    def setUp(self) -> None:
        self.captured = self.enterContext(mock.patch.object(run_affected_tests, "run_captured"))
        self.progress = self.enterContext(mock.patch.object(run_affected_tests, "log_progress"))

    def query_with(
        self, proc: subprocess.CompletedProcess[str], stream: bool = False
    ) -> tuple[str, ...] | None:
        """Queries the RDEPS_SCOPE targets with run_captured returning proc."""
        self.captured.return_value = proc
        return run_affected_tests.query_emulator_dependents(RDEPS_SCOPE.bazel_targets, stream)

    def stderr_of_query(self, stream: bool) -> str:
        """Returns what the query writes to stderr when Bazel prints a loading line."""
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            self.query_with(completed(stderr="Loading: 0 packages\n"), stream)
        return stderr.getvalue()

    def test_runs_an_offline_query_for_targets_reaching_the_emulator(self) -> None:
        self.query_with(completed())
        expr = (
            "let t = set(//tools:a_test //internal/x:x_test) in "
            "$t intersect rdeps($t, //internal/storage/spanner/emulator:all)"
        )
        expected_cmd = ["bazel", "query", "--fetch=false", "--noimplicit_deps", expr]
        self.captured.assert_called_once_with(expected_cmd)

    def test_returns_the_dependent_targets(self) -> None:
        dependents = self.query_with(completed("//internal/x:x_test\n"))
        self.assertEqual(dependents, EMULATOR_DEPENDENTS)

    def test_query_failure_returns_none(self) -> None:
        self.assertIsNone(self.query_with(completed(returncode=7, stderr="ERROR: no repo\n")))

    def test_query_failure_logs_the_last_stderr_line(self) -> None:
        self.query_with(completed(returncode=7, stderr="Loading\nERROR: no repo\n"))
        self.assertIn("ERROR: no repo", self.progress.call_args.args[0])

    def test_streaming_copies_query_stderr_to_stderr(self) -> None:
        self.assertEqual(self.stderr_of_query(stream=True), "Loading: 0 packages\n")

    def test_quiet_query_keeps_its_stderr_off_stderr(self) -> None:
        self.assertEqual(self.stderr_of_query(stream=False), "")


class TargetsNeedEmulatorTest(TempLogTestCase):
    """Tests for deciding whether the selected targets need the Spanner emulator."""

    def setUp(self) -> None:
        super().setUp()
        self.query = self.enterContext(
            mock.patch.object(run_affected_tests, "query_emulator_dependents", return_value=())
        )

    def need_emulator(self, scope: ResolvedScope) -> bool:
        """Returns whether scope needs the emulator, without streaming."""
        return run_affected_tests.targets_need_emulator(scope, stream=False)

    def test_full_suite_needs_the_emulator(self) -> None:
        self.assertTrue(self.need_emulator(CORE_INFRA_SCOPE))

    def test_full_suite_skips_the_query(self) -> None:
        self.need_emulator(CORE_INFRA_SCOPE)
        self.query.assert_not_called()

    def test_no_targets_need_no_emulator(self) -> None:
        self.assertFalse(self.need_emulator(NONE_SCOPE))

    def test_dependent_target_needs_the_emulator(self) -> None:
        self.query.return_value = EMULATOR_DEPENDENTS
        self.assertTrue(self.need_emulator(RDEPS_SCOPE))

    def test_targets_without_dependents_need_no_emulator(self) -> None:
        self.assertFalse(self.need_emulator(RDEPS_SCOPE))

    def test_failed_query_assumes_the_emulator_is_needed(self) -> None:
        self.query.return_value = None
        self.assertTrue(self.need_emulator(RDEPS_SCOPE))


class ParseEmulatorAddressTest(unittest.TestCase):
    """Tests for validating SPANNER_EMULATOR_HOST values."""

    def test_splits_host_and_port(self) -> None:
        address = run_affected_tests.parse_emulator_address("localhost:9010")
        self.assertEqual(address, ("localhost", 9010))

    def test_strips_ipv6_brackets(self) -> None:
        self.assertEqual(run_affected_tests.parse_emulator_address("[::1]:9010"), ("::1", 9010))

    def test_malformed_addresses_raise_handled_error(self) -> None:
        for address in MALFORMED_EMULATOR_HOSTS:
            with self.subTest(address=address), self.assertRaises(HandledError):
                run_affected_tests.parse_emulator_address(address)


class IsPortOpenTest(unittest.TestCase):
    """Tests for probing TCP ports, with socket.create_connection mocked."""

    def setUp(self) -> None:
        self.connect = self.enterContext(mock.patch.object(socket, "create_connection"))

    def test_accepted_connection_means_open(self) -> None:
        self.assertTrue(run_affected_tests.is_port_open("localhost", 9010))

    def test_dials_the_address_with_the_connect_timeout(self) -> None:
        run_affected_tests.is_port_open("localhost", 9010)
        timeout = run_affected_tests.EMULATOR_CONNECT_TIMEOUT_SECONDS
        self.connect.assert_called_once_with(("localhost", 9010), timeout=timeout)

    def test_refused_connection_means_closed(self) -> None:
        self.connect.side_effect = ConnectionRefusedError
        self.assertFalse(run_affected_tests.is_port_open("localhost", 9010))

    def test_timed_out_connection_means_closed(self) -> None:
        self.connect.side_effect = TimeoutError
        self.assertFalse(run_affected_tests.is_port_open("localhost", 9010))


class EnsureEmulatorReachableTest(unittest.TestCase):
    """Tests for the Spanner emulator reachability pre-flight."""

    def setUp(self) -> None:
        self.dial = self.enterContext(
            mock.patch.object(run_affected_tests, "is_port_open", return_value=True)
        )

    def unreachable_error(self, env: dict[str, str]) -> HandledError:
        """Returns the HandledError raised for env when nothing listens."""
        self.dial.return_value = False
        with self.assertRaises(HandledError) as ctx:
            run_affected_tests.ensure_emulator_reachable(env)
        return ctx.exception

    def test_dials_the_default_address_when_unset(self) -> None:
        run_affected_tests.ensure_emulator_reachable({})
        self.dial.assert_called_once_with("localhost", 9010)

    def test_dials_spanner_emulator_host_when_set(self) -> None:
        run_affected_tests.ensure_emulator_reachable({"SPANNER_EMULATOR_HOST": "10.0.0.5:9999"})
        self.dial.assert_called_once_with("10.0.0.5", 9999)

    def test_unreachable_emulator_message_names_the_address(self) -> None:
        self.assertIn("localhost:9010", self.unreachable_error({}).message)

    def test_unreachable_emulator_hint_pins_the_ci_image(self) -> None:
        self.assertIn("emulator:1.5.56", self.unreachable_error({}).instruction)

    def test_malformed_address_fails_without_dialing(self) -> None:
        self.unreachable_error({"SPANNER_EMULATOR_HOST": "localhost"})
        self.dial.assert_not_called()


class BuildBazelTestCommandTest(unittest.TestCase):
    """Tests for the `bazel test` command line."""

    def build(self, env: dict[str, str]) -> list[str]:
        """Builds the command for one target with env."""
        bep_path = Path("/tmp/bep.json")
        return run_affected_tests.build_bazel_test_command(["//a:a_test"], env, bep_path)

    def test_includes_ci_flags_bep_file_and_targets(self) -> None:
        ci_flags = ["--test_output=errors", "--experimental_ui_max_stdouterr_bytes=2097152"]
        ci_flags += ["--build_tests_only", "--test_timeout=60,300,300,300"]
        bep_flag = "--build_event_json_file=/tmp/bep.json"
        self.assertEqual(self.build({}), ["bazel", "test", *ci_flags, bep_flag, "--", "//a:a_test"])

    def test_passes_a_set_spanner_emulator_host_to_tests(self) -> None:
        cmd = self.build({"SPANNER_EMULATOR_HOST": "localhost:9999"})
        self.assertIn("--test_env=SPANNER_EMULATOR_HOST=localhost:9999", cmd)

    def test_ignores_an_empty_spanner_emulator_host(self) -> None:
        cmd = self.build({"SPANNER_EMULATOR_HOST": ""})
        self.assertFalse([arg for arg in cmd if arg.startswith("--test_env")])


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
            run_affected_tests.run_bazel_tests(["//a:a_test"], {}, self.log)
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
        self.warmup = self.enterContext(
            mock.patch.object(run_affected_tests, "run_logged", return_value=0)
        )
        self.dependents = self.enterContext(
            mock.patch.object(run_affected_tests, "query_emulator_dependents", return_value=())
        )
        self.dial = self.enterContext(
            mock.patch.object(run_affected_tests, "is_port_open", return_value=False)
        )
        self.bazel = self.enterContext(
            mock.patch.object(run_affected_tests, "run_bazel_tests", return_value=PASSING_OUTCOME)
        )

    def run_affected(self, scope: ResolvedScope, **args: Any) -> dict[str, Any]:
        """Runs run_affected_tests on scope with the given CLI overrides and an empty env."""
        with mock.patch.object(run_affected_tests, "resolve_scope", return_value=scope):
            return run_affected_tests.run_affected_tests(make_args(**args), env={})

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
            run_affected_tests.run_affected_tests(make_args(verbose=True), env={})
        res.assert_called_once_with(CHANGED_FILES, True)

    def test_reports_no_fallback_for_a_narrowed_scope(self) -> None:
        result = self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.assertEqual((result["fallback"], result["fallback_reason"]), (False, None))

    def test_reports_a_fallback_that_survives_the_warmup(self) -> None:
        result = self.run_affected(FALLBACK_SCOPE, dry_run=True)
        expected = (True, run_affected_tests.FALLBACK_EXPLANATION)
        self.assertEqual((result["fallback"], result["fallback_reason"]), expected)

    def test_reports_that_the_warmup_ran(self) -> None:
        self.assertTrue(self.run_affected(FALLBACK_SCOPE, dry_run=True)["warmup_ran"])

    def test_dry_run_reports_the_log_of_the_warmup(self) -> None:
        def warm_up(cmd: Sequence[str], log: run_affected_tests.BazelLog, **kwargs: Any) -> int:
            log.ensure_path()
            return 0

        self.warmup.side_effect = warm_up
        result = self.run_affected(FALLBACK_SCOPE, dry_run=True)
        self.assertEqual(result["log_path"], str(self.log_path))

    def test_reports_emulator_required_for_dependent_targets(self) -> None:
        self.dependents.return_value = EMULATOR_DEPENDENTS
        self.assertTrue(self.run_affected(RDEPS_SCOPE, dry_run=True)["emulator_required"])

    def test_dry_run_never_dials_a_required_unreachable_emulator(self) -> None:
        self.dependents.return_value = EMULATOR_DEPENDENTS
        self.run_affected(RDEPS_SCOPE, dry_run=True)
        self.dial.assert_not_called()

    def test_unreachable_required_emulator_fails_before_bazel(self) -> None:
        self.dependents.return_value = EMULATOR_DEPENDENTS
        with self.assertRaises(HandledError):
            self.run_affected(RDEPS_SCOPE)
        self.bazel.assert_not_called()

    def test_skip_emulator_check_runs_bazel_without_dialing(self) -> None:
        self.dependents.return_value = EMULATOR_DEPENDENTS
        self.run_affected(RDEPS_SCOPE, skip_emulator_check=True)
        self.dial.assert_not_called()

    def test_all_runs_the_full_backend_suite(self) -> None:
        self.dial.return_value = True
        self.run_affected(RDEPS_SCOPE, all=True)
        self.bazel.assert_called_once_with(FULL_SUITE, {}, mock.ANY)

    def test_nothing_to_run_skips_bazel(self) -> None:
        self.run_affected(NONE_SCOPE)
        self.bazel.assert_not_called()

    def test_nothing_to_run_reports_no_passed_targets(self) -> None:
        self.assertEqual(self.run_affected(NONE_SCOPE)["passed_targets"], [])

    def test_runs_bazel_on_the_resolved_targets(self) -> None:
        self.run_affected(RDEPS_SCOPE)
        self.bazel.assert_called_once_with(RDEPS_SCOPE.bazel_targets, {}, mock.ANY)

    def test_output_is_quiet_when_stderr_is_not_a_terminal(self) -> None:
        self.run_affected(RDEPS_SCOPE)
        self.assertFalse(self.bazel.call_args.args[2].stream)

    def test_verbose_streams_bazel_output(self) -> None:
        self.run_affected(RDEPS_SCOPE, verbose=True)
        self.assertTrue(self.bazel.call_args.args[2].stream)

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
        expected = {
            "base_ref": "origin/main",
            "all": False,
            "dry_run": False,
            "no_warmup": False,
            "skip_emulator_check": False,
            "verbose": False,
        }
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
        code, stdout, _ = self.run_main(lambda args, env: {"dry_run": args.dry_run})
        self.assertEqual((code, json.loads(stdout)["result"]), (0, {"dry_run": True}))

    def test_passes_the_process_environment(self) -> None:
        _, stdout, _ = self.run_main(lambda args, env: {"os_environ": env is os.environ})
        self.assertTrue(json.loads(stdout)["result"]["os_environ"])

    def test_handled_error_prints_details_and_exits_one(self) -> None:
        error = HandledError("tests failed", "fix them", {"log_path": "/l"})
        code, _, stderr = self.run_main(error)
        self.assertEqual((code, json.loads(stderr)["details"]), (1, {"log_path": "/l"}))

    def test_unexpected_error_exits_two(self) -> None:
        code, _, stderr = self.run_main(RuntimeError("boom"))
        self.assertEqual((code, json.loads(stderr)["error_type"]), (2, "unhandled"))


if __name__ == "__main__":
    unittest.main()
