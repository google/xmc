#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for examples_import_smoke."""

import dataclasses
import io
import json
import pathlib
import sys
import tempfile
import textwrap
import unittest
from contextlib import redirect_stdout
from unittest import mock

from tools.nightly_tests import examples_import_smoke as smoke

_WORKFLOW = textwrap.dedent(
    """\
    jobs:
      run-and-verify-examples:
        strategy:
          matrix:
            example:
              - id: 'ok_job'
                example_path: 'examples/ok_job'
                entrypoint: 'launch.py'
                args: '--queue=x'
              - id: 'ok_job_again'
                example_path: 'examples/ok_job'
                entrypoint: 'launch.py'
                args: '--queue=x'
              - id: 'broken_job'
                example_path: 'examples/broken_job'
                entrypoint: 'create.py'
              - id: 'ui_only'
                example_path: ''
                entrypoint: ''
        env:
          EXAMPLE_PATH: '${{ matrix.example.example_path }}'
          ENTRYPOINT: '${{ matrix.example.entrypoint }}'
        steps:
          - name: 'Launch Example Workload'
            run: echo launch
          - name: 'Step input sharing a matrix key name (must be ignored)'
            with:
              example_path: 'not/a/matrix/entry'
    """
)

_OK_ENTRYPOINT = textwrap.dedent(
    """\
    import sys
    import job_flags  # resolved via the example dir on PYTHONPATH
    if sys.argv != ["launch.py"]:
        raise SystemExit(f"unexpected argv: {sys.argv}")
    if __name__ == "__main__":
        raise SystemExit("main must not run during smoke checks")
    """
)
# Fails to import, then prints a cleanup warning after the traceback via atexit.
_BROKEN_ENTRYPOINT = textwrap.dedent(
    """\
    import atexit, sys
    atexit.register(lambda: print("WARNING: trailing cleanup log", file=sys.stderr))
    from json import resolve_auth_headers
    if __name__ == "__main__":
        pass
    """
)
# Defines absl flags at module level, like simple_job/launch.py.
_FLAGS_ENTRYPOINT = textwrap.dedent(
    """\
    from absl import flags
    flags.DEFINE_string("queue", None, "Target queue.")
    flags.DEFINE_integer("num_work_units", 1, "Work units.")
    if __name__ == "__main__":
        raise SystemExit("main must not run during smoke checks")
    """
)
_GUARD_SNIPPETS = {
    "standard": 'if __name__ == "__main__":\n    pass\n',
    "reversed": 'if "__main__" == __name__:\n    pass\n',
    "missing": "import sys\n",
    "nested_only": 'def f():\n    if __name__ == "__main__":\n        pass\n',
    "not_equal": 'if __name__ != "__main__":\n    pass\n',
}


class ParseEntrypointsTest(unittest.TestCase):
    """Tests for parse_entrypoints."""

    def test_dedupes_and_skips_empty_paths(self) -> None:
        """Repeated entries collapse and UI-only entries are skipped."""
        self.assertEqual(
            smoke.parse_entrypoints(_WORKFLOW),
            [
                smoke.Entrypoint("examples/ok_job", "launch.py", "--queue=x"),
                smoke.Entrypoint("examples/broken_job", "create.py"),
            ],
        )

    def test_same_entrypoint_with_different_args_is_kept(self) -> None:
        """Matrix rows that differ only in args are checked separately."""
        workflow = _WORKFLOW.replace("args: '--queue=x'", "args: '--queue=y'", 1)
        self.assertEqual(
            [e.args for e in smoke.parse_entrypoints(workflow)], ["--queue=y", "--queue=x", ""]
        )

    def test_parse_verify_modules_includes_ui_only_entries(self) -> None:
        """Verify modules are collected for every entry, deduped, honoring the filter."""
        workflow = textwrap.dedent(
            """\
            matrix:
              example:
                - id: 'a'
                  example_path: 'examples/a'
                  entrypoint: 'launch.py'
                  verify_module: 'pkg.a_verify'
                - id: 'a_again'
                  example_path: 'examples/a'
                  entrypoint: 'launch.py'
                  verify_module: 'pkg.a_verify'
                - id: 'ui'
                  example_path: ''
                  entrypoint: ''
                  verify_module: 'pkg.ui_verify'
            """
        )
        self.assertEqual(smoke.parse_verify_modules(workflow), ["pkg.a_verify", "pkg.ui_verify"])
        self.assertEqual(smoke.parse_verify_modules(workflow, frozenset({"ui"})), ["pkg.ui_verify"])

    def test_expand_args(self) -> None:
        """Known placeholders expand (env first, then default); unknown ones are rejected."""
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(smoke.expand_args("--queue=${QUEUE_NAME}"), "--queue=cpu-queue")
        with mock.patch.dict("os.environ", {"QUEUE_NAME": "gpu-queue"}):
            self.assertEqual(smoke.expand_args("--queue=${QUEUE_NAME}"), "--queue=gpu-queue")
        with mock.patch.dict("os.environ", {"QUEUE_NAME": ""}):
            self.assertEqual(smoke.expand_args("--queue=${QUEUE_NAME}"), "--queue=")
        with self.assertRaisesRegex(ValueError, "IMAGE_TAG"):
            smoke.expand_args("--image=${IMAGE_TAG}")

    def test_has_main_guard(self) -> None:
        """Only a top-level `__name__ == "__main__"` comparison counts as a guard."""
        expected = {
            "standard": True,
            "reversed": True,
            "missing": False,
            "nested_only": False,
            "not_equal": False,
        }
        with tempfile.TemporaryDirectory() as tmp_str:
            for name, source in _GUARD_SNIPPETS.items():
                with self.subTest(name):
                    script = pathlib.Path(tmp_str) / f"{name}.py"
                    script.write_text(source, encoding="utf-8")
                    self.assertEqual(smoke._has_main_guard(script), expected[name])

    def test_tolerates_formatting_variations(self) -> None:
        """Quoting style, key order, interleaved keys, and comments do not matter."""
        workflow = textwrap.dedent(
            """\
            matrix:
              example:
                - id: 'double_quoted'
                  example_path: "examples/a"
                  args: ''
                  entrypoint: "launch.py"
                - entrypoint: create.py  # reordered and unquoted
                  id: 'reordered'
                  example_path: examples/b
            """
        )
        self.assertEqual(
            smoke.parse_entrypoints(workflow),
            [
                smoke.Entrypoint("examples/a", "launch.py"),
                smoke.Entrypoint("examples/b", "create.py"),
            ],
        )

    def test_rejects_example_path_without_entrypoint(self) -> None:
        """A matrix entry missing its entrypoint fails loudly instead of being dropped."""
        broken = _WORKFLOW.replace("entrypoint: 'create.py'", "args: ''")
        with self.assertRaisesRegex(ValueError, "examples/broken_job"):
            smoke.parse_entrypoints(broken)

    def test_rejects_entrypoint_without_example_path(self) -> None:
        """A matrix entry with an entrypoint but empty example_path fails loudly."""
        broken = _WORKFLOW.replace("example_path: 'examples/broken_job'", "example_path: ''")
        with self.assertRaisesRegex(ValueError, "create.py"):
            smoke.parse_entrypoints(broken)

    def test_keys_outside_list_items_are_not_attributed(self) -> None:
        """Keys after a list ends, or before a later block's first item, stay unattached."""
        workflow = textwrap.dedent(
            """\
            jobs:
              a:
                strategy:
                  matrix:
                    example:
                      - id: 'first'
                        example_path: 'examples/a'
                        entrypoint: 'launch.py'
                    id: 'not_an_item_key'
              b:
                strategy:
                  matrix:
                    id: 'stray'
                    entrypoint: 'stray.py'
                    example:
                      - id: 'second'
                        example_path: 'examples/b'
                        entrypoint: 'launch.py'
            """
        )
        self.assertEqual(
            smoke.parse_matrix_items(workflow),
            [
                {"id": "first", "example_path": "examples/a", "entrypoint": "launch.py"},
                {"id": "second", "example_path": "examples/b", "entrypoint": "launch.py"},
            ],
        )


class SmokeExecutionTest(unittest.TestCase):
    """Tests for smoke_entrypoint and main using real subprocesses."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = pathlib.Path(tmp.name)
        ok_dir = self.repo / "examples" / "ok_job"
        ok_dir.mkdir(parents=True)
        (ok_dir / "launch.py").write_text(_OK_ENTRYPOINT, encoding="utf-8")
        (ok_dir / "job_flags.py").write_text("", encoding="utf-8")
        broken_dir = self.repo / "examples" / "broken_job"
        broken_dir.mkdir(parents=True)
        (broken_dir / "create.py").write_text(_BROKEN_ENTRYPOINT, encoding="utf-8")
        self.workflow = self.repo / "nightly.yml"
        self.workflow.write_text(_WORKFLOW, encoding="utf-8")
        self.python = pathlib.Path(sys.executable)

    def test_ok_entrypoint_imports_without_running_main(self) -> None:
        """A healthy entrypoint passes with argv[0] set to the script, without running main."""
        result = smoke.smoke_entrypoint(
            self.python, self.repo, smoke.Entrypoint("examples/ok_job", "launch.py")
        )
        self.assertEqual(result, smoke.SmokeResult("examples/ok_job", "launch.py", ok=True))

    def test_import_error_survives_trailing_stderr(self) -> None:
        """The ImportError stays in the reported tail even when stderr has trailing logs."""
        result = smoke.smoke_entrypoint(
            self.python, self.repo, smoke.Entrypoint("examples/broken_job", "create.py")
        )
        self.assertFalse(result.ok)
        self.assertIn("ImportError", result.error)
        self.assertIn("resolve_auth_headers", result.error)
        self.assertTrue(result.error.endswith("WARNING: trailing cleanup log"))
        self.assertLessEqual(len(result.error.splitlines()), smoke._STDERR_TAIL_LINES)

    def test_missing_entrypoint_is_reported_per_entry(self) -> None:
        """A renamed or deleted example directory fails that entry without crashing."""
        result = smoke.smoke_entrypoint(
            self.python, self.repo, smoke.Entrypoint("examples/renamed_job", "launch.py")
        )
        self.assertFalse(result.ok)
        self.assertIn("entrypoint not found", result.error)

    def test_non_importable_entrypoint_name_is_reported(self) -> None:
        """Entrypoints that cannot be imported by module name fail with a clear reason."""
        for entrypoint in ("my-launch.py", "launch", "2launch.py"):
            with self.subTest(entrypoint=entrypoint):
                result = smoke.smoke_entrypoint(
                    self.python, self.repo, smoke.Entrypoint("examples/ok_job", entrypoint)
                )
                self.assertFalse(result.ok)
                self.assertIn("not an importable module file", result.error)

    def test_blocking_import_times_out(self) -> None:
        """An entrypoint that blocks at import time fails with a timeout, not a hang."""
        slow_dir = self.repo / "examples" / "slow_job"
        slow_dir.mkdir(parents=True)
        (slow_dir / "launch.py").write_text(
            'import time\ntime.sleep(30)\nif __name__ == "__main__":\n    pass\n',
            encoding="utf-8",
        )
        with mock.patch.object(smoke, "_IMPORT_TIMEOUT_SECONDS", 1):
            result = smoke.smoke_entrypoint(
                self.python, self.repo, smoke.Entrypoint("examples/slow_job", "launch.py")
            )
        self.assertFalse(result.ok)
        self.assertIn("timed out after 1s", result.error)

    def test_unstartable_interpreter_is_reported_per_entry(self) -> None:
        """A bad --venv-python fails each entry cleanly instead of raising."""
        result = smoke.smoke_entrypoint(
            self.repo / "no_such_python",
            self.repo,
            smoke.Entrypoint("examples/ok_job", "launch.py"),
        )
        self.assertFalse(result.ok)
        self.assertIn("failed to start", result.error)

    def _write_example(self, name: str, source: str) -> smoke.Entrypoint:
        example_dir = self.repo / "examples" / name
        example_dir.mkdir(parents=True)
        (example_dir / "launch.py").write_text(source, encoding="utf-8")
        return smoke.Entrypoint(f"examples/{name}", "launch.py")

    def test_absl_args_are_parsed_against_module_flags(self) -> None:
        """Matrix args must match the module's absl flags; unknown flags fail."""
        entry = self._write_example("flags_job", _FLAGS_ENTRYPOINT)
        cases = (
            ("valid", "--queue=${QUEUE_NAME} --num_work_units=3", True, ""),
            ("renamed_flag", "--queue_name=${QUEUE_NAME}", False, "queue_name"),
            ("bad_type", "--num_work_units=three", False, "num_work_units"),
        )
        for name, args, expected_ok, expected_error in cases:
            with self.subTest(name):
                result = smoke.smoke_entrypoint(
                    self.python, self.repo, dataclasses.replace(entry, args=args)
                )
                self.assertEqual(result.ok, expected_ok, result.error)
                self.assertIn(expected_error, result.error)

    def test_args_are_ignored_for_modules_without_absl_flags(self) -> None:
        """argparse-style entrypoints (flags parsed inside main) skip args parsing."""
        entry = smoke.Entrypoint(
            "examples/ok_job", "launch.py", "--endpoint=x --create-experiments"
        )
        self.assertTrue(smoke.smoke_entrypoint(self.python, self.repo, entry).ok)

    def test_unknown_placeholder_fails_entry(self) -> None:
        """An args placeholder the nightly step does not define fails that entry."""
        entry = smoke.Entrypoint("examples/ok_job", "launch.py", "--image=${IMAGE_TAG}")
        result = smoke.smoke_entrypoint(self.python, self.repo, entry)
        self.assertFalse(result.ok)
        self.assertIn("IMAGE_TAG", result.error)

    def test_unparseable_entrypoint_fails_that_entry(self) -> None:
        """Syntax errors or non-UTF-8 sources fail one entry instead of crashing the run."""
        cases = (
            ("syntax_job", b"def broken(:\n", "failed to parse"),
            ("encoding_job", b"\xff\xfe not utf-8\n", "failed to parse"),
        )
        for name, source, expected_error in cases:
            with self.subTest(name):
                example_dir = self.repo / "examples" / name
                example_dir.mkdir(parents=True)
                (example_dir / "launch.py").write_bytes(source)
                result = smoke.smoke_entrypoint(
                    self.python, self.repo, smoke.Entrypoint(f"examples/{name}", "launch.py")
                )
                self.assertFalse(result.ok)
                self.assertIn(expected_error, result.error)

    def test_missing_main_guard_fails_without_importing(self) -> None:
        """An entrypoint without a `__main__` guard fails before it is imported."""
        entry = self._write_example("unguarded_job", "raise SystemExit('imported!')\n")
        result = smoke.smoke_entrypoint(self.python, self.repo, entry)
        self.assertFalse(result.ok)
        self.assertIn("guard", result.error)
        self.assertNotIn("imported!", result.error)

    def test_verify_modules_import_from_repo_root(self) -> None:
        """Verify modules import by name from the repo root; broken ones fail."""
        pkg = self.repo / "verify_pkg"
        pkg.mkdir()
        (pkg / "__init__.py").write_text("", encoding="utf-8")
        (pkg / "ok_verify.py").write_text("import verify_pkg\n", encoding="utf-8")
        (pkg / "broken_verify.py").write_text("from json import nope\n", encoding="utf-8")
        ok = smoke.smoke_verify_module(self.python, self.repo, "verify_pkg.ok_verify")
        broken = smoke.smoke_verify_module(self.python, self.repo, "verify_pkg.broken_verify")
        self.assertEqual((ok.ok, ok.kind), (True, "verify_module"), ok.error)
        self.assertFalse(broken.ok)
        self.assertIn("nope", broken.error)

    def test_relative_paths_are_anchored(self) -> None:
        """Relative --repo-root/--venv-python are made absolute before cwd changes."""
        args = smoke._parse_args(["--repo-root", ".", "--venv-python", "venv/bin/python3"])
        self.assertEqual(args.repo_root, pathlib.Path.cwd().resolve())
        self.assertEqual(args.venv_python, pathlib.Path.cwd() / "venv" / "bin" / "python3")

    def test_main_reports_json_and_exit_code(self) -> None:
        """main prints a JSON report and returns 1 when any entrypoint fails."""
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            code = smoke.main(
                [
                    "--venv-python",
                    str(self.python),
                    "--repo-root",
                    str(self.repo),
                    "--workflow",
                    str(self.workflow),
                ]
            )
        self.assertEqual(code, 1)
        report = json.loads(stdout.getvalue())
        self.assertEqual([r["ok"] for r in report["results"]], [True, False])

    def test_main_returns_2_without_entrypoints(self) -> None:
        """main returns 2 when the workflow yields no entrypoints."""
        self.workflow.write_text("jobs: {}\n", encoding="utf-8")
        with redirect_stdout(io.StringIO()):
            code = smoke.main(["--workflow", str(self.workflow)])
        self.assertEqual(code, 2)

    def test_main_returns_2_for_missing_workflow(self) -> None:
        """main reports an unreadable workflow as JSON with exit code 2, not a traceback."""
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            code = smoke.main(["--workflow", str(self.repo / "missing.yml")])
        self.assertEqual(code, 2)
        self.assertIn("missing.yml", json.loads(stdout.getvalue())["error"])

    def test_list_example_paths_prints_unique_matrix_paths(self) -> None:
        """--list-example-paths prints each matrix example path once, without importing."""
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            code = smoke.main(["--workflow", str(self.workflow), "--list-example-paths"])
        self.assertEqual(code, 0)
        self.assertEqual(stdout.getvalue().split(), ["examples/ok_job", "examples/broken_job"])

    def test_examples_filter_selects_matrix_ids_case_insensitively(self) -> None:
        """--examples-filter keeps only the listed matrix ids, like the nightly filter."""
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            code = smoke.main(
                [
                    "--venv-python",
                    str(self.python),
                    "--repo-root",
                    str(self.repo),
                    "--workflow",
                    str(self.workflow),
                    "--examples-filter",
                    " OK_JOB ,unknown",
                ]
            )
        self.assertEqual(code, 0)
        report = json.loads(stdout.getvalue())
        self.assertEqual([r["example_path"] for r in report["results"]], ["examples/ok_job"])

    def test_examples_filter_with_only_ui_entries_succeeds_empty(self) -> None:
        """A filter selecting only UI-only entries checks and lists nothing, and succeeds."""
        base = ["--workflow", str(self.workflow), "--examples-filter", "ui_only"]
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            self.assertEqual(smoke.main(base), 0)
        self.assertEqual(json.loads(stdout.getvalue()), {"results": []})
        stdout = io.StringIO()
        with redirect_stdout(stdout):
            self.assertEqual(smoke.main([*base, "--list-example-paths"]), 0)
        self.assertEqual(stdout.getvalue().strip(), "")


if __name__ == "__main__":
    unittest.main()
