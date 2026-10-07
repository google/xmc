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

"""Unit tests for the check_proto_breaking tool."""

import argparse
import contextlib
import io
import json
import subprocess
import sys
import tempfile
import unittest
from collections.abc import Iterator
from pathlib import Path
from typing import Any
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import check_proto_breaking  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402

BASE_COMMIT = "0123456789abcdef0123456789abcdef01234567"
REPO = Path("/repo")
SCRATCH = Path("/scratch")
CQUERY_OUTPUT = "bazel-out/k8-fastbuild/bin/api/all_protos_descriptor_set.pb"
FIELD_FINDING = {
    "path": "api/protos/xid/messages.proto",
    "start_line": 1,
    "type": "FIELD_NO_DELETE",
    "message": 'Previously present field "2" with name "name" on message "Xid" was deleted.',
}
FILE_FINDING = {
    "type": "FILE_NO_DELETE",
    "message": 'Previously present file "api/protos/metrics/metrics.proto" was deleted.',
}

Response = subprocess.CompletedProcess[str] | BaseException


def _completed(
    returncode: int = 0, stdout: str = "", stderr: str = ""
) -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


class FakeProcess:
    """subprocess.Popen double that replays a canned response on its merged output."""

    def __init__(self, response: subprocess.CompletedProcess[str]) -> None:
        self.stdout = io.StringIO(response.stdout + response.stderr)
        self.returncode = response.returncode

    def __enter__(self) -> "FakeProcess":
        return self

    def __exit__(self, *exc_info: object) -> None:
        return None


class FakeRunner:
    """Test double for run_command and subprocess.Popen serving canned responses.

    Responses are keyed by command prefixes such as ("git", "worktree", "remove"); the
    longest matching prefix wins. Exception responses are raised, and run_command's
    `check` semantics are mirrored by raising CalledProcessError on failures.
    """

    def __init__(self, responses: dict[tuple[str, ...], Response] | None = None) -> None:
        self.responses = responses or {}
        self.calls: list[tuple[list[str], Any]] = []

    def respond(self, cmd: list[str], cwd: Any) -> subprocess.CompletedProcess[str]:
        self.calls.append((list(cmd), cwd))
        prefixes = [prefix for prefix in self.responses if tuple(cmd[: len(prefix)]) == prefix]
        response = self.responses[max(prefixes, key=len)] if prefixes else _completed()
        if isinstance(response, BaseException):
            raise response
        return response

    def __call__(self, cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
        response = self.respond(cmd, kwargs.get("cwd"))
        if kwargs.get("check", True) and response.returncode != 0:
            raise subprocess.CalledProcessError(
                response.returncode, cmd, response.stdout, response.stderr
            )
        return response

    def popen(self, cmd: list[str], **kwargs: Any) -> FakeProcess:
        return FakeProcess(self.respond(cmd, kwargs.get("cwd")))

    def commands(self) -> list[list[str]]:
        return [cmd for cmd, _ in self.calls]


@contextlib.contextmanager
def fake_processes(runner: FakeRunner) -> Iterator[io.StringIO]:
    """Routes the tool's subprocesses to runner and yields its captured stderr."""
    stderr = io.StringIO()
    with (
        mock.patch.object(check_proto_breaking, "run_command", runner),
        mock.patch.object(check_proto_breaking.subprocess, "Popen", runner.popen),
        contextlib.redirect_stderr(stderr),
    ):
        yield stderr


class BuildParserTest(unittest.TestCase):
    """Tests for command-line parsing."""

    def test_defaults(self):
        args = check_proto_breaking.build_parser().parse_args([])
        self.assertIsNone(args.base_ref)
        self.assertEqual(args.bazel_flags, [])

    def test_repeatable_bazel_flags_and_base_ref(self):
        args = check_proto_breaking.build_parser().parse_args(
            [
                "--base-ref",
                "HEAD^1",
                "--bazel-flag=--config=ci",
                "--bazel-flag=--repository_cache=/cache",
            ]
        )
        self.assertEqual(args.base_ref, "HEAD^1")
        self.assertEqual(args.bazel_flags, ["--config=ci", "--repository_cache=/cache"])


class ResolveBufBinaryTest(unittest.TestCase):
    """Tests for locating the buf executable."""

    @mock.patch.object(check_proto_breaking.shutil, "which", return_value="/usr/bin/buf")
    def test_finds_buf_on_path(self, mock_which):
        self.assertEqual(check_proto_breaking.resolve_buf_binary(), "/usr/bin/buf")
        mock_which.assert_called_once_with("buf")

    @mock.patch.object(check_proto_breaking.shutil, "which", return_value=None)
    def test_missing_buf_raises_install_instruction(self, _):
        with self.assertRaises(HandledError) as ctx:
            check_proto_breaking.resolve_buf_binary()
        self.assertIn(check_proto_breaking.BUF_INSTALL_URL, ctx.exception.instruction)


class ResolveBaseCommitTest(unittest.TestCase):
    """Tests for resolving the base revision."""

    def test_defaults_to_merge_base_with_origin_main(self):
        runner = FakeRunner({("git", "merge-base"): _completed(stdout=f"{BASE_COMMIT}\n")})
        with fake_processes(runner):
            commit = check_proto_breaking.resolve_base_commit(REPO, None)
        self.assertEqual(commit, BASE_COMMIT)
        self.assertEqual(runner.calls, [(["git", "merge-base", "HEAD", "origin/main"], REPO)])

    def test_explicit_ref_is_verified_as_commit(self):
        runner = FakeRunner({("git", "rev-parse"): _completed(stdout=f"{BASE_COMMIT}\n")})
        with fake_processes(runner):
            commit = check_proto_breaking.resolve_base_commit(REPO, "HEAD^1")
        self.assertEqual(commit, BASE_COMMIT)
        self.assertEqual(runner.commands(), [["git", "rev-parse", "--verify", "HEAD^1^{commit}"]])

    def test_unknown_ref_raises_handled_error(self):
        failure = _completed(returncode=128, stderr="fatal: Needed a single revision")
        runner = FakeRunner({("git", "rev-parse"): failure})
        with fake_processes(runner):
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.resolve_base_commit(REPO, "no-such-ref")
        self.assertIn("Needed a single revision", ctx.exception.message)


class BuildDescriptorSetTest(unittest.TestCase):
    """Tests for building and extracting the aggregate descriptor set."""

    def setUp(self):
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self.temp_path = Path(temp_dir.name)
        self.execution_root = self.temp_path / "execroot"
        self.destination = self.temp_path / "head.binpb"

    def test_copies_output_streams_build_log_and_forwards_flags(self):
        output = self.execution_root / CQUERY_OUTPUT
        output.parent.mkdir(parents=True)
        output.write_bytes(b"descriptor-bytes")
        runner = FakeRunner(
            {
                ("bazel", "build"): _completed(stderr="INFO: Build completed successfully\n"),
                ("bazel", "cquery"): _completed(stdout=f"{CQUERY_OUTPUT}\n"),
                ("bazel", "info"): _completed(stdout=f"{self.execution_root}\n"),
            }
        )
        with fake_processes(runner) as stderr:
            check_proto_breaking.build_descriptor_set(REPO, ["--config=ci"], self.destination)
        self.assertEqual(self.destination.read_bytes(), b"descriptor-bytes")
        self.assertEqual(stderr.getvalue(), "INFO: Build completed successfully\n")
        target = check_proto_breaking.DESCRIPTOR_SET_TARGET
        self.assertEqual(
            runner.commands(),
            [
                ["bazel", "build", "--config=ci", target],
                ["bazel", "cquery", "--config=ci", "--output=files", target],
                ["bazel", "info", "execution_root"],
            ],
        )
        self.assertTrue(all(cwd == REPO for _, cwd in runner.calls))

    def test_build_failure_reports_last_40_log_lines(self):
        log = "".join(f"log line {number}\n" for number in range(1, 51))
        runner = FakeRunner({("bazel", "build"): _completed(returncode=1, stderr=log)})
        with fake_processes(runner) as stderr:
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.build_descriptor_set(REPO, [], self.destination)
        header, *tail = ctx.exception.message.splitlines()
        self.assertEqual(header, f"'bazel build' failed in {REPO}:")
        self.assertEqual(tail, [f"log line {number}" for number in range(11, 51)])
        self.assertEqual(stderr.getvalue(), log)
        self.assertFalse(self.destination.exists())

    def test_query_failure_reports_stderr(self):
        failure = _completed(returncode=2, stderr="ERROR: Unrecognized option: --bad")
        runner = FakeRunner({("bazel", "cquery"): failure})
        with fake_processes(runner):
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.build_descriptor_set(REPO, ["--bad"], self.destination)
        self.assertIn("'bazel cquery' failed", ctx.exception.message)
        self.assertIn("Unrecognized option: --bad", ctx.exception.message)

    def test_failure_hint_reproduces_the_failed_command(self):
        target = check_proto_breaking.DESCRIPTOR_SET_TARGET
        hints = {
            "build": f"bazel build --config=ci {target}",
            "cquery": f"bazel cquery --config=ci --output=files {target}",
            "info": "bazel info execution_root",
        }
        for command, hint in hints.items():
            with self.subTest(command=command):
                runner = FakeRunner({("bazel", "cquery"): _completed(stdout=f"{CQUERY_OUTPUT}\n")})
                runner.responses[("bazel", command)] = _completed(returncode=1, stderr="ERROR")
                with fake_processes(runner):
                    with self.assertRaises(HandledError) as ctx:
                        check_proto_breaking.build_descriptor_set(
                            REPO, ["--config=ci"], self.destination
                        )
                expected = f"Fix the Bazel error above; reproduce it with: {hint}"
                self.assertEqual(ctx.exception.instruction, expected)

    def test_unexpected_output_count_raises(self):
        runner = FakeRunner({("bazel", "cquery"): _completed(stdout="a.pb\nb.pb\n")})
        with fake_processes(runner):
            with self.assertRaises(HandledError):
                check_proto_breaking.build_descriptor_set(REPO, [], self.destination)


class EnsureFullCoverageTest(unittest.TestCase):
    """Tests for the guard against proto_library targets missing from the aggregate."""

    def test_runs_plain_query_with_anchored_kind_patterns(self):
        runner = FakeRunner()
        with fake_processes(runner):
            check_proto_breaking.ensure_full_coverage(REPO)
        query = check_proto_breaking.UNCOVERED_PROTOS_QUERY
        self.assertEqual(runner.calls, [(["bazel", "query", query], REPO)])
        self.assertEqual(query.count('kind("^proto_library rule$", '), 2)
        self.assertIn(f"deps({check_proto_breaking.DESCRIPTOR_SET_TARGET})", query)

    def test_uncovered_files_raise_with_listing(self):
        stdout = "//api/protos/new:a.proto\n//api/protos/new:b.proto\n"
        runner = FakeRunner({("bazel", "query"): _completed(stdout=stdout)})
        with fake_processes(runner):
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.ensure_full_coverage(REPO)
        self.assertIn("misses 2 proto file(s)", ctx.exception.message)
        self.assertIn("//api/protos/new:a.proto\n//api/protos/new:b.proto", ctx.exception.message)
        self.assertIn(check_proto_breaking.DESCRIPTOR_SET_TARGET, ctx.exception.instruction)

    def test_query_failure_hint_reproduces_the_quoted_query(self):
        runner = FakeRunner({("bazel", "query"): _completed(returncode=7, stderr="ERROR: bad")})
        with fake_processes(runner):
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.ensure_full_coverage(REPO)
        query = check_proto_breaking.UNCOVERED_PROTOS_QUERY
        expected = f"Fix the Bazel error above; reproduce it with: bazel query '{query}'"
        self.assertEqual(ctx.exception.instruction, expected)


class BaseWorktreeTest(unittest.TestCase):
    """Tests for the temporary base worktree lifecycle."""

    WORKTREE = SCRATCH / "base"
    LIFECYCLE = [
        (["git", "worktree", "add", "--detach", str(WORKTREE), BASE_COMMIT], REPO),
        (["bazel", "clean", "--expunge"], WORKTREE),
        (["git", "worktree", "remove", "--force", str(WORKTREE)], REPO),
    ]

    def test_creates_and_removes_worktree(self):
        runner = FakeRunner()
        with fake_processes(runner) as stderr:
            with check_proto_breaking.base_worktree(REPO, BASE_COMMIT, SCRATCH) as worktree:
                self.assertEqual(worktree, self.WORKTREE)
        self.assertEqual(runner.calls, self.LIFECYCLE)
        self.assertEqual(stderr.getvalue(), "")

    def test_removal_failure_does_not_mask_body_error(self):
        failure = _completed(returncode=1, stderr="fatal: cannot remove a locked worktree")
        runner = FakeRunner({("git", "worktree", "remove"): failure})
        with fake_processes(runner) as stderr:
            with self.assertRaises(HandledError) as ctx:
                with check_proto_breaking.base_worktree(REPO, BASE_COMMIT, SCRATCH):
                    raise HandledError("base build failed", "fix it")
        self.assertEqual(ctx.exception.message, "base build failed")
        self.assertEqual(runner.calls, self.LIFECYCLE)
        self.assertIn("locked worktree", stderr.getvalue())
        self.assertIn("git worktree prune", stderr.getvalue())

    def test_expunge_failure_warns_and_still_removes_worktree(self):
        runner = FakeRunner({("bazel", "clean"): _completed(returncode=37)})
        with fake_processes(runner) as stderr:
            with check_proto_breaking.base_worktree(REPO, BASE_COMMIT, SCRATCH):
                pass
        self.assertEqual(runner.calls, self.LIFECYCLE)
        warning = f"'bazel clean --expunge' failed in {self.WORKTREE} (exit code 37)."
        self.assertIn(warning, stderr.getvalue())

    def test_expunge_os_error_warns_and_still_removes_worktree(self):
        runner = FakeRunner({("bazel", "clean"): OSError("bazel not found")})
        with fake_processes(runner) as stderr:
            with check_proto_breaking.base_worktree(REPO, BASE_COMMIT, SCRATCH):
                pass
        self.assertEqual(runner.calls, self.LIFECYCLE)
        self.assertIn("'bazel clean --expunge' failed", stderr.getvalue())
        self.assertIn("bazel not found", stderr.getvalue())

    def test_removal_os_error_does_not_mask_body_error(self):
        runner = FakeRunner({("git", "worktree", "remove"): OSError("git not found")})
        with fake_processes(runner) as stderr:
            with self.assertRaises(HandledError) as ctx:
                with check_proto_breaking.base_worktree(REPO, BASE_COMMIT, SCRATCH):
                    raise HandledError("base build failed", "fix it")
        self.assertEqual(ctx.exception.message, "base build failed")
        self.assertEqual(runner.calls, self.LIFECYCLE)
        self.assertIn("(git not found). Run 'git worktree prune'", stderr.getvalue())


class RunBufBreakingTest(unittest.TestCase):
    """Tests for invoking buf and parsing its findings."""

    HEAD = SCRATCH / "head.binpb"
    BASE = SCRATCH / "base.binpb"
    CONFIG = REPO / "api" / "buf-breaking.yaml"

    def _run(self, response: Response) -> tuple[list[dict[str, Any]], FakeRunner, str]:
        runner = FakeRunner({("/opt/buf", "breaking"): response})
        with fake_processes(runner) as stderr:
            findings = check_proto_breaking.run_buf_breaking(
                "/opt/buf", self.HEAD, self.BASE, self.CONFIG
            )
        return findings, runner, stderr.getvalue()

    def test_compatible_change_returns_no_findings(self):
        findings, runner, _ = self._run(_completed())
        self.assertEqual(findings, [])
        cmd, cwd = runner.calls[0]
        self.assertEqual(
            cmd,
            [
                "/opt/buf",
                "breaking",
                str(self.HEAD),
                "--against",
                str(self.BASE),
                "--config",
                str(self.CONFIG),
                "--error-format=json",
            ],
        )
        self.assertEqual(cwd, SCRATCH)

    def test_breaking_change_returns_parsed_findings(self):
        stdout = f"{json.dumps(FIELD_FINDING)}\n{json.dumps(FILE_FINDING)}\n"
        findings, _, _ = self._run(_completed(returncode=100, stdout=stdout))
        self.assertEqual(findings, [FIELD_FINDING, FILE_FINDING])

    def test_non_json_lines_are_skipped_with_a_warning(self):
        stdout = f"buf: deprecated flag\n{json.dumps(FIELD_FINDING)}\n"
        findings, _, stderr = self._run(_completed(returncode=100, stdout=stdout))
        self.assertEqual(findings, [FIELD_FINDING])
        self.assertIn("WARNING: Skipping non-JSON buf output: buf: deprecated flag", stderr)

    def test_findings_exit_code_without_findings_raises(self):
        for stdout in ("\n", "panic: not JSON\n"):
            with self.subTest(stdout=stdout):
                with self.assertRaises(HandledError) as ctx:
                    self._run(_completed(returncode=100, stdout=stdout))
                self.assertIn("exit code 100 without reporting findings", ctx.exception.message)

    def test_buf_failure_raises_handled_error(self):
        with self.assertRaises(HandledError) as ctx:
            self._run(_completed(returncode=1, stderr="Failure: invalid config"))
        self.assertIn("invalid config", ctx.exception.message)


@mock.patch.object(check_proto_breaking, "run_buf_breaking", return_value=[])
@mock.patch.object(check_proto_breaking, "ensure_full_coverage")
@mock.patch.object(check_proto_breaking, "build_descriptor_set")
class CompareAgainstBaseTest(unittest.TestCase):
    """Tests for orchestrating both builds, the coverage guard, and the buf comparison."""

    def test_checks_coverage_between_head_and_base_builds(self, mock_build, mock_cov, mock_buf):
        calls = mock.Mock()
        calls.attach_mock(mock_build, "build")
        calls.attach_mock(mock_cov, "coverage")
        runner = FakeRunner()
        with fake_processes(runner):
            findings = check_proto_breaking.compare_against_base(
                REPO, BASE_COMMIT, "/opt/buf", ["--config=ci"]
            )
        self.assertEqual(findings, [])
        scratch = mock_build.call_args_list[0].args[2].parent
        self.assertEqual(
            calls.mock_calls,
            [
                mock.call.build(REPO, ["--config=ci"], scratch / "head.binpb"),
                mock.call.coverage(REPO),
                mock.call.build(scratch / "base", ["--config=ci"], scratch / "base.binpb"),
            ],
        )
        mock_buf.assert_called_once_with(
            "/opt/buf",
            scratch / "head.binpb",
            scratch / "base.binpb",
            REPO / "api" / "buf-breaking.yaml",
        )
        self.assertIn(
            ["git", "worktree", "remove", "--force", str(scratch / "base")], runner.commands()
        )

    def test_uncovered_protos_fail_before_base_checkout(self, mock_build, mock_cov, mock_buf):
        mock_cov.side_effect = HandledError("uncovered protos", "add them")
        runner = FakeRunner()
        with fake_processes(runner):
            with self.assertRaises(HandledError):
                check_proto_breaking.compare_against_base(REPO, BASE_COMMIT, "/opt/buf", [])
        mock_build.assert_called_once()
        self.assertEqual(runner.calls, [])
        mock_buf.assert_not_called()

    def test_removes_worktree_when_base_build_fails(self, mock_build, _, mock_buf):
        mock_build.side_effect = [None, HandledError("base build failed", "fix it")]
        runner = FakeRunner()
        with fake_processes(runner):
            with self.assertRaises(HandledError) as ctx:
                check_proto_breaking.compare_against_base(REPO, BASE_COMMIT, "/opt/buf", [])
        self.assertEqual(ctx.exception.message, "base build failed")
        self.assertEqual(runner.commands()[-1][:4], ["git", "worktree", "remove", "--force"])
        mock_buf.assert_not_called()


class CheckProtoBreakingTest(unittest.TestCase):
    """Tests for the top-level check outcome."""

    ARGS = argparse.Namespace(base_ref="HEAD^1", bazel_flags=["--config=ci"])

    def setUp(self):
        patches = {
            "resolve_buf_binary": mock.patch.object(
                check_proto_breaking, "resolve_buf_binary", return_value="/opt/buf"
            ),
            "resolve_base_commit": mock.patch.object(
                check_proto_breaking, "resolve_base_commit", return_value=BASE_COMMIT
            ),
            "compare": mock.patch.object(
                check_proto_breaking, "compare_against_base", return_value=[]
            ),
        }
        self.mocks = {name: patcher.start() for name, patcher in patches.items()}
        self.addCleanup(mock.patch.stopall)

    def test_passes_without_findings(self):
        result = check_proto_breaking.check_proto_breaking(self.ARGS)
        self.assertEqual(
            result,
            {"status": "passed", "base_commit": BASE_COMMIT, "config": "api/buf-breaking.yaml"},
        )
        self.mocks["resolve_base_commit"].assert_called_once_with(
            check_proto_breaking._REPO_ROOT, "HEAD^1"
        )
        self.mocks["compare"].assert_called_once_with(
            check_proto_breaking._REPO_ROOT, BASE_COMMIT, "/opt/buf", ["--config=ci"]
        )

    def test_breaking_findings_raise_with_details(self):
        self.mocks["compare"].return_value = [FIELD_FINDING, FILE_FINDING]
        with self.assertRaises(HandledError) as ctx:
            check_proto_breaking.check_proto_breaking(self.ARGS)
        self.assertIn("Found 2 breaking protobuf change(s)", ctx.exception.message)
        self.assertIn(f"[FIELD_NO_DELETE] {FIELD_FINDING['path']}: ", ctx.exception.message)
        self.assertIn("[FILE_NO_DELETE] Previously present file", ctx.exception.message)
        self.assertIn("reserve the numbers and names", ctx.exception.instruction)

    def test_missing_buf_fails_before_resolving_base(self):
        self.mocks["resolve_buf_binary"].side_effect = HandledError("buf missing", "install")
        with self.assertRaises(HandledError):
            check_proto_breaking.check_proto_breaking(self.ARGS)
        self.mocks["resolve_base_commit"].assert_not_called()


if __name__ == "__main__":
    unittest.main()
