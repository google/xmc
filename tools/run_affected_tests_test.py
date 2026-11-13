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
import io
import json
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from typing import Any
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.append(str(_REPO_ROOT))

from tools import run_affected_tests as rat  # noqa: E402
from tools.common.changed_files import ChangedFilesError  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402

RDEPS_SCOPE: dict[str, Any] = {
    "run_backend": True,
    "run_ui": False,
    "run_helm": True,
    "backend_mode": "rdeps",
    "bazel_targets": "//pkg/a:a_test //pkg/b:b_test",
    "reason": "discovered 2 affected bazel test targets",
    "changed_files": ["deploy/helm/values.yaml", "pkg/a/a.go"],
}
UI_ONLY_SCOPE: dict[str, Any] = {
    "run_backend": False,
    "run_ui": True,
    "run_helm": False,
    "backend_mode": "none",
    "bazel_targets": "",
    "reason": "isolated frontend UI modifications",
    "changed_files": ["cmd/ui/src/app/app.component.ts"],
}
HELM_ACTION = ".github/actions/run-chart-tests/action.yml"
UI_ACTION = ".github/actions/run-ui-tests/action.yml"
PASSING = rat.BazelTestOutcome(0, "SUCCESS", ("//pkg/a:a_test", "//pkg/b:b_test"), (), (), ())
FAILING = rat.BazelTestOutcome(
    3,
    "TESTS_FAILED",
    ("//pkg/a:a_test",),
    (rat.FailedTarget("//pkg/b:b_test", "FAILED", ("/logs/b/test.log",)),),
    (),
    (),
)


def parse(*argv: str) -> argparse.Namespace:
    """Parses argv with the tool's parser."""
    return rat.build_parser().parse_args(list(argv))


def test_summary_event(label: str, status: str, *log_uris: str) -> dict[str, Any]:
    """Returns a BEP testSummary event."""
    summary = {"overallStatus": status, "failed": [{"uri": uri} for uri in log_uris]}
    return {"id": {"testSummary": {"label": label}}, "testSummary": summary}


def write_build_events(path: Path, *lines: str | dict[str, Any]) -> None:
    """Writes BEP JSON lines; strings are written verbatim, e.g. to model a torn line."""
    text = "\n".join(line if isinstance(line, str) else json.dumps(line) for line in lines)
    path.write_text(text + "\n", encoding="utf-8")


class BuildParserTest(unittest.TestCase):
    def test_defaults_to_local_mode_with_ci_test_flags_only(self):
        args = parse()
        self.assertEqual(
            (args.ci, args.base_ref, args.repository_cache, args.bazel_flags),
            (False, None, None, []),
        )
        self.assertEqual(
            (args.emulator_wait_seconds, args.skip_emulator_check, args.dry_run),
            (0.0, False, False),
        )

    def test_bazel_flags_accumulate_in_order(self):
        args = parse("--bazel-flag=--config=ci", "--bazel-flag=--test_timeout=90,300,300,300")
        self.assertEqual(args.bazel_flags, ["--config=ci", "--test_timeout=90,300,300,300"])

    def test_local_and_ci_are_mutually_exclusive(self):
        with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as ctx:
            parse("--local", "--ci")
        self.assertEqual(ctx.exception.code, 2)


class ResolveScopeTest(unittest.TestCase):
    @mock.patch.object(rat.resolve_test_scope, "resolve_local_scope", return_value=RDEPS_SCOPE)
    def test_local_mode_scopes_checkout_against_origin_main(self, mock_local):
        self.assertIs(rat.resolve_scope(parse(), {}), RDEPS_SCOPE)
        mock_local.assert_called_once_with("origin/main", rat._REPO_ROOT, repository_cache=None)

    @mock.patch.object(rat.resolve_test_scope, "resolve_local_scope", return_value=RDEPS_SCOPE)
    def test_local_mode_passes_base_ref_and_repository_cache(self, mock_local):
        rat.resolve_scope(
            parse("--local", "--base-ref", "upstream/dev", "--repository-cache", "/c"), {}
        )
        mock_local.assert_called_once_with("upstream/dev", rat._REPO_ROOT, repository_cache="/c")

    @mock.patch.object(rat.resolve_test_scope, "resolve_ci_scope", return_value=RDEPS_SCOPE)
    def test_ci_mode_scopes_event_through_resolver_parser(self, mock_ci):
        env = {"GITHUB_EVENT_NAME": "pull_request"}
        self.assertIs(
            rat.resolve_scope(parse("--ci", "--repository-cache", "/c"), env), RDEPS_SCOPE
        )
        resolver_args, passed_env = mock_ci.call_args.args
        self.assertIs(passed_env, env)
        self.assertEqual(
            (resolver_args.repository_cache, resolver_args.base, resolver_args.files),
            ("/c", None, None),
        )
        self.assertEqual((resolver_args.local, resolver_args.disable_rdeps), (False, False))

    @mock.patch.object(rat.resolve_test_scope, "resolve_ci_scope", return_value=RDEPS_SCOPE)
    def test_ci_mode_passes_base_ref(self, mock_ci):
        rat.resolve_scope(parse("--ci", "--base-ref", "abc123"), {})
        resolver_args = mock_ci.call_args.args[0]
        self.assertEqual((resolver_args.base, resolver_args.repository_cache), ("abc123", None))


class DescribeScopeTest(unittest.TestCase):
    def test_reports_targets_and_other_suites(self):
        self.assertEqual(
            rat.describe_scope(parse(), RDEPS_SCOPE),
            {
                "mode": "local",
                "changed_files": ["deploy/helm/values.yaml", "pkg/a/a.go"],
                "backend_mode": "rdeps",
                "reason": "discovered 2 affected bazel test targets",
                "bazel_targets": ["//pkg/a:a_test", "//pkg/b:b_test"],
                "other_suites": [HELM_ACTION],
                "dry_run": False,
            },
        )

    def test_ci_scope_has_no_changed_files_and_lists_ui_first(self):
        ci_scope = {k: v for k, v in RDEPS_SCOPE.items() if k != "changed_files"}
        report = rat.describe_scope(
            parse("--ci", "--dry-run"),
            ci_scope | {"run_ui": True},
        )
        self.assertEqual(
            (
                report["mode"],
                report["changed_files"],
                report["bazel_targets"],
                report["other_suites"],
                report["dry_run"],
            ),
            ("ci", None, ["//pkg/a:a_test", "//pkg/b:b_test"], [UI_ACTION, HELM_ACTION], True),
        )


class EmulatorCheckTest(unittest.TestCase):
    def test_parse_emulator_address_accepts_host_port_forms(self):
        cases = {
            "localhost:9010": ("localhost", 9010),
            "127.0.0.1:9020": ("127.0.0.1", 9020),
            "[::1]:9010": ("::1", 9010),
        }
        for address, expected in cases.items():
            with self.subTest(address=address):
                self.assertEqual(rat.parse_emulator_address(address), expected)

    def test_parse_emulator_address_rejects_malformed_addresses(self):
        for address in ("localhost", ":9010", "host:0", "host:65536", "host:port"):
            with self.subTest(address=address), self.assertRaises(HandledError) as ctx:
                rat.parse_emulator_address(address)
            self.assertIn(f"SPANNER_EMULATOR_HOST='{address}'", ctx.exception.message)

    @mock.patch.object(rat.time, "sleep")
    @mock.patch.object(rat.socket, "create_connection")
    def test_wait_for_port_returns_on_first_connection(self, mock_connect, mock_sleep):
        self.assertTrue(rat.wait_for_port("localhost", 9010, 15))
        mock_connect.assert_called_once_with(("localhost", 9010), timeout=1.0)
        mock_sleep.assert_not_called()

    @mock.patch.object(rat.time, "sleep")
    @mock.patch.object(rat.socket, "create_connection", side_effect=ConnectionRefusedError)
    def test_wait_for_port_without_wait_tries_once(self, mock_connect, mock_sleep):
        self.assertFalse(rat.wait_for_port("localhost", 9010, 0))
        mock_connect.assert_called_once()
        mock_sleep.assert_not_called()

    @mock.patch.object(rat.time, "monotonic", side_effect=[0.0, 1.0, 2.25])
    @mock.patch.object(rat.time, "sleep")
    @mock.patch.object(rat.socket, "create_connection")
    def test_wait_for_port_retries_until_connected(self, mock_connect, mock_sleep, _):
        mock_connect.side_effect = [ConnectionRefusedError, OSError, mock.MagicMock()]
        self.assertTrue(rat.wait_for_port("localhost", 9010, 2.5))
        self.assertEqual(mock_connect.call_count, 3)
        self.assertEqual(mock_sleep.call_args_list, [mock.call(1.0), mock.call(0.25)])

    @mock.patch.object(rat.time, "monotonic", side_effect=[0.0, 1.0, 2.0])
    @mock.patch.object(rat.time, "sleep")
    @mock.patch.object(rat.socket, "create_connection", side_effect=ConnectionRefusedError)
    def test_wait_for_port_gives_up_at_deadline(self, mock_connect, mock_sleep, _):
        self.assertFalse(rat.wait_for_port("localhost", 9010, 2))
        self.assertEqual((mock_connect.call_count, mock_sleep.call_count), (2, 1))

    @mock.patch.object(rat, "wait_for_port", return_value=True)
    def test_ensure_emulator_reachable_checks_configured_address(self, mock_wait):
        rat.ensure_emulator_reachable({"SPANNER_EMULATOR_HOST": "127.0.0.1:9011"}, 15)
        mock_wait.assert_called_once_with("127.0.0.1", 9011, 15)

    @mock.patch.object(rat, "wait_for_port", return_value=False)
    def test_ensure_emulator_reachable_explains_how_to_start_it(self, mock_wait):
        with self.assertRaises(HandledError) as ctx:
            rat.ensure_emulator_reachable({}, 0)
        mock_wait.assert_called_once_with("localhost", 9010, 0)
        self.assertIn("localhost:9010", ctx.exception.message)
        self.assertIn("docs/developer.md", ctx.exception.instruction)
        self.assertIn("--skip-emulator-check", ctx.exception.instruction)


class BazelTestCommandTest(unittest.TestCase):
    def test_extra_flags_follow_ci_test_flags(self):
        cmd = rat.build_bazel_test_command(
            ["//...", "-//cmd/ui/..."],
            ["--config=ci", "--test_timeout=90,300,300,300"],
            {},
            Path("/tmp/bep.json"),
        )
        self.assertEqual(
            cmd,
            [
                "bazel",
                "test",
                "--test_output=errors",
                "--experimental_ui_max_stdouterr_bytes=2097152",
                "--build_tests_only",
                "--test_timeout=60,300,300,300",
                "--build_event_json_file=/tmp/bep.json",
                "--config=ci",
                "--test_timeout=90,300,300,300",
                "--",
                "//...",
                "-//cmd/ui/...",
            ],
        )

    def test_passes_only_non_default_emulator_address_to_tests(self):
        for default_host in ("localhost:9010", "127.0.0.1:9010"):
            env = {"SPANNER_EMULATOR_HOST": default_host}
            cmd = rat.build_bazel_test_command(["//a:a_test"], [], env, Path("/tmp/bep.json"))
            self.assertFalse(any(a.startswith("--test_env=") for a in cmd), default_host)
        custom_env = {"SPANNER_EMULATOR_HOST": "127.0.0.1:9020"}
        custom_cmd = rat.build_bazel_test_command(
            ["//a:a_test"], [], custom_env, Path("/tmp/bep.json")
        )
        self.assertIn(
            "--test_env=SPANNER_EMULATOR_HOST=127.0.0.1:9020",
            custom_cmd[: custom_cmd.index("--")],
        )


class ReadTestOutcomeTest(unittest.TestCase):
    def test_reads_statuses_logs_build_errors_and_exit_name(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            bep_path = Path(tmp_dir) / "bep.json"
            write_build_events(
                bep_path,
                test_summary_event("//a:a_test", "PASSED"),
                test_summary_event("//b:b_test", "FLAKY"),
                test_summary_event("//c:c_test", "FAILED", "file:///logs/c%20dir/test.log"),
                {"id": {"targetCompleted": {"label": "//c:c_test"}}, "completed": {}},
                test_summary_event("//d:d_test", "NO_STATUS"),
                {"id": {"targetCompleted": {"label": "//e:e_test"}}, "completed": {}},
                {
                    "id": {"targetCompleted": {"label": "//f:f_test"}},
                    "completed": {"success": True},
                },
                {"id": {}, "aborted": {"description": "no such target '//x:y_test'"}},
                {"id": {}, "aborted": {"reason": "SKIPPED"}},
                {"id": {}, "finished": {"exitCode": {"name": "TESTS_FAILED", "code": 3}}},
                '{"id": {"progress"',
            )
            stderr = io.StringIO()
            with redirect_stderr(stderr):
                outcome = rat.read_test_outcome(bep_path, 3)
        self.assertEqual(
            outcome,
            rat.BazelTestOutcome(
                3,
                "TESTS_FAILED",
                ("//a:a_test", "//b:b_test"),
                (
                    rat.FailedTarget("//c:c_test", "FAILED", ("/logs/c dir/test.log",)),
                    rat.FailedTarget("//e:e_test", "FAILED_TO_BUILD"),
                ),
                ("//d:d_test",),
                ("no such target '//x:y_test'",),
            ),
        )
        self.assertIn("Skipped a malformed line", stderr.getvalue())

    def test_streams_dict_events_and_skips_incomplete_fields(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            bep_path = Path(tmp_dir) / "bep.json"
            write_build_events(
                bep_path,
                "[1, 2, 3]",
                {"id": {"testSummary": {}}, "testSummary": {"failed": [{}]}},
                {"id": {"targetCompleted": {}}, "completed": {}},
                {"id": {}, "finished": {}},
                test_summary_event("//ok:ok_test", "PASSED"),
            )
            stream = rat.read_build_events(bep_path)
            self.assertIs(iter(stream), stream)
            outcome = rat.read_test_outcome(bep_path, 0)
        self.assertEqual(outcome.passed_targets, ("//ok:ok_test",))

    def test_missing_file_yields_unknown_outcome(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            outcome = rat.read_test_outcome(Path(tmp_dir) / "missing.json", 37)
        self.assertEqual(outcome, rat.BazelTestOutcome(37, "UNKNOWN", (), (), (), ()))


class RunBazelTestsTest(unittest.TestCase):
    @mock.patch.object(rat, "run_command")
    def test_streams_bazel_output_and_reads_its_build_events(self, mock_run):
        bep_paths: list[Path] = []

        def fake_bazel(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
            bep_flag = next(arg for arg in cmd if arg.startswith("--build_event_json_file="))
            bep_paths.append(Path(bep_flag.split("=", 1)[1]))
            write_build_events(bep_paths[0], test_summary_event("//a:a_test", "PASSED"))
            return subprocess.CompletedProcess(cmd, 0)

        mock_run.side_effect = fake_bazel
        outcome = rat.run_bazel_tests(["//a:a_test"], ["--config=ci"], {"FOO": "bar"})
        self.assertEqual((outcome.exit_code, outcome.passed_targets), (0, ("//a:a_test",)))
        self.assertEqual(
            mock_run.call_args.kwargs,
            {"env": {"FOO": "bar"}, "check": False, "cwd": rat._REPO_ROOT, "stream": sys.stderr},
        )
        self.assertIn("--config=ci", mock_run.call_args.args[0])
        self.assertFalse(bep_paths[0].exists())

    @mock.patch.object(rat, "run_command", side_effect=FileNotFoundError("bazel"))
    def test_missing_bazel_is_a_handled_error(self, _):
        with self.assertRaises(HandledError) as ctx:
            rat.run_bazel_tests(["//a:a_test"], [], {})
        self.assertIn("`bazel` is not installed", ctx.exception.message)


class RaiseForTestFailureTest(unittest.TestCase):
    def test_success_does_not_raise(self):
        rat.raise_for_test_failure(PASSING, {"backend_mode": "rdeps"})

    def test_failure_carries_concise_report_and_outcome(self):
        report = {"backend_mode": "rdeps", "bazel_targets": ["//pkg/a:a_test", "//pkg/b:b_test"]}
        with self.assertRaises(HandledError) as ctx:
            rat.raise_for_test_failure(FAILING, report)
        self.assertIn("TESTS_FAILED (exit code 3)", ctx.exception.message)
        self.assertIn("//pkg/b:b_test (FAILED)", ctx.exception.message)
        details = json.loads(json.dumps(ctx.exception.details))
        self.assertEqual(details["backend_mode"], "rdeps")
        self.assertNotIn("bazel_targets", details)
        self.assertNotIn("passed_targets", details)
        self.assertEqual(
            details["failed_targets"],
            [{"target": "//pkg/b:b_test", "status": "FAILED", "test_logs": ["/logs/b/test.log"]}],
        )

    def test_build_failure_points_to_build_errors(self):
        outcome = rat.BazelTestOutcome(1, "BUILD_FAILURE", (), (), ("//a:a_test",), ("boom",))
        with self.assertRaises(HandledError) as ctx:
            rat.raise_for_test_failure(outcome, {})
        self.assertIn("none, see build_errors", ctx.exception.message)
        self.assertIn("targets that did not run: 1", ctx.exception.message)


@mock.patch.object(rat, "run_bazel_tests", return_value=PASSING)
@mock.patch.object(rat, "ensure_emulator_reachable")
@mock.patch.object(rat, "resolve_scope", return_value=RDEPS_SCOPE)
class RunAffectedTestsTest(unittest.TestCase):
    def _run(self, args: argparse.Namespace, env: dict[str, str]) -> tuple[dict[str, Any], str]:
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            result = rat.run_affected_tests(args, env)
        return result, stderr.getvalue()

    def test_dry_run_reports_scope_without_running(self, mock_scope, mock_emulator, mock_bazel):
        args = parse("--dry-run")
        result, log = self._run(args, {})
        self.assertEqual(result, rat.describe_scope(args, RDEPS_SCOPE) | {"passed_targets": []})
        mock_scope.assert_called_once_with(args, {})
        mock_emulator.assert_not_called()
        mock_bazel.assert_not_called()
        self.assertIn("Backend scope: rdeps (discovered 2 affected bazel test targets)", log)
        self.assertIn(f"CI also runs {HELM_ACTION}", log)

    def test_scope_without_targets_runs_nothing(self, mock_scope, mock_emulator, mock_bazel):
        mock_scope.return_value = UI_ONLY_SCOPE
        result, _ = self._run(parse(), {})
        self.assertEqual((result["other_suites"], result["passed_targets"]), ([UI_ACTION], []))
        mock_emulator.assert_not_called()
        mock_bazel.assert_not_called()

    def test_runs_targets_after_emulator_check(self, mock_scope, mock_emulator, mock_bazel):
        args = parse(
            "--repository-cache=/c", "--bazel-flag=--config=ci", "--emulator-wait-seconds=15"
        )
        env = {"SPANNER_EMULATOR_HOST": "127.0.0.1:9010"}
        result, _ = self._run(args, env)
        self.assertEqual(result["passed_targets"], ["//pkg/a:a_test", "//pkg/b:b_test"])
        mock_emulator.assert_called_once_with(env, 15.0)
        mock_bazel.assert_called_once_with(
            ["//pkg/a:a_test", "//pkg/b:b_test"], ["--repository_cache=/c", "--config=ci"], env
        )

    def test_skip_emulator_check(self, mock_scope, mock_emulator, mock_bazel):
        self._run(parse("--skip-emulator-check"), {})
        mock_emulator.assert_not_called()
        mock_bazel.assert_called_once()

    def test_unreachable_emulator_stops_before_bazel(self, mock_scope, mock_emulator, mock_bazel):
        mock_emulator.side_effect = HandledError("emulator down", "start it")
        with self.assertRaises(HandledError):
            self._run(parse(), {})
        mock_bazel.assert_not_called()

    def test_test_failures_raise_with_details(self, mock_scope, mock_emulator, mock_bazel):
        mock_bazel.return_value = FAILING
        with self.assertRaises(HandledError) as ctx:
            self._run(parse(), {})
        details = ctx.exception.details or {}
        self.assertNotIn("bazel_targets", details)
        self.assertNotIn("passed_targets", details)
        self.assertEqual(details["exit_name"], "TESTS_FAILED")

    @mock.patch.object(
        rat,
        "run_diff_cover",
        return_value={"compare_branch": "origin/main", "fail_under": 85, "report": {}},
    )
    def test_coverage_mode_runs_bazel_coverage_and_diff_cover(
        self, mock_diff_cover, mock_scope, mock_emulator, mock_bazel
    ):
        args = parse("--coverage", "--diff-cover-fail-under=85")
        result, _ = self._run(args, {})
        mock_bazel.assert_called_once_with(
            ["//pkg/a:a_test", "//pkg/b:b_test"], [], {}, coverage=True
        )
        mock_diff_cover.assert_called_once_with("origin/main", 85, {})
        self.assertEqual(result["diff_cover"]["fail_under"], 85)


class DiffCoverTest(unittest.TestCase):
    def test_build_bazel_test_command_with_coverage(self):
        cmd = rat.build_bazel_test_command(["//pkg:t"], [], {}, Path("/tmp/b.json"), coverage=True)
        self.assertEqual(cmd[:2], ["bazel", "coverage"])
        self.assertIn("--combined_report=lcov", cmd)

    def test_resolve_compare_branch_precedence(self):
        self.assertEqual(rat.resolve_compare_branch(parse("--base-ref=dev"), {}), "dev")
        self.assertEqual(
            rat.resolve_compare_branch(parse("--ci"), {"GITHUB_BASE_REF": "release"}),
            "origin/release",
        )
        self.assertEqual(
            rat.resolve_compare_branch(parse("--ci"), {"GITHUB_BASE_REF": "origin/main"}),
            "origin/main",
        )
        self.assertEqual(rat.resolve_compare_branch(parse(), {}), "origin/main")

    def test_run_diff_cover_skips_when_disabled_or_missing_lcov(self):
        res = rat.run_diff_cover("origin/main", 0, {})
        self.assertTrue(res["skipped"])
        with tempfile.TemporaryDirectory() as tmp:
            missing = Path(tmp) / "missing.dat"
            res2 = rat.run_diff_cover("origin/main", 80, {}, lcov_path=missing)
            self.assertTrue(res2["skipped"])

    @mock.patch.object(rat, "run_command")
    def test_run_diff_cover_passes_and_reads_report(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp:
            lcov = Path(tmp) / "coverage.dat"
            lcov.write_text("SF:foo.py\nDA:1,1\nend_of_record\n", encoding="utf-8")

            def fake_diff_cover(cmd, **_kwargs):
                for arg in cmd:
                    if arg.startswith("--json-report="):
                        Path(arg.split("=", 1)[1]).write_text(
                            json.dumps({"total_percent_covered": 100}), encoding="utf-8"
                        )
                return subprocess.CompletedProcess(cmd, 0)

            mock_run.side_effect = fake_diff_cover
            out = rat.run_diff_cover("origin/main", 80, {}, lcov_path=lcov)
            self.assertEqual(out["report"]["total_percent_covered"], 100)

    @mock.patch.object(rat, "run_command")
    def test_run_diff_cover_raises_handled_error_when_below_threshold(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp:
            lcov = Path(tmp) / "coverage.dat"
            lcov.write_text("SF:foo.py\nDA:1,0\nend_of_record\n", encoding="utf-8")
            mock_run.return_value = subprocess.CompletedProcess(["diff-cover"], 1)
            with self.assertRaises(HandledError) as ctx:
                rat.run_diff_cover("origin/main", 80, {}, lcov_path=lcov)
            self.assertIn("below 80%", ctx.exception.message)

    @mock.patch.object(rat, "run_command", side_effect=FileNotFoundError("diff-cover"))
    def test_run_diff_cover_missing_binary_is_handled_error(self, _mock_run):
        with tempfile.TemporaryDirectory() as tmp:
            lcov = Path(tmp) / "coverage.dat"
            lcov.write_text("SF:foo.py\nDA:1,1\nend_of_record\n", encoding="utf-8")
            with self.assertRaises(HandledError) as ctx:
                rat.run_diff_cover("origin/main", 80, {}, lcov_path=lcov)
            self.assertIn("diff-cover", ctx.exception.message)


class MainTest(unittest.TestCase):
    def _main(self, *argv: str) -> tuple[int | str | None, str, str]:
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            mock.patch.object(sys, "argv", ["run_affected_tests.py", *argv]),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
            self.assertRaises(SystemExit) as ctx,
        ):
            rat.main()
        return ctx.exception.code, stdout.getvalue(), stderr.getvalue()

    @mock.patch.object(rat.resolve_test_scope, "resolve_local_scope", return_value=RDEPS_SCOPE)
    def test_prints_only_json_on_stdout(self, _):
        code, stdout, stderr = self._main("--local", "--dry-run")
        self.assertEqual(code, 0)
        output = json.loads(stdout)
        self.assertEqual((output["status"], output["result"]["backend_mode"]), ("success", "rdeps"))
        self.assertIn("Backend scope: rdeps", stderr)

    @mock.patch.object(rat.resolve_test_scope, "resolve_local_scope")
    def test_git_failure_is_a_handled_error(self, mock_local):
        mock_local.side_effect = ChangedFilesError("`git merge-base origin/main HEAD` failed: bad")
        code, stdout, stderr = self._main()
        self.assertEqual((code, stdout), (1, ""))
        output = json.loads(stderr)
        self.assertEqual(output["error_type"], "handled")
        self.assertIn("git fetch origin", output["instruction"])


if __name__ == "__main__":
    unittest.main()
