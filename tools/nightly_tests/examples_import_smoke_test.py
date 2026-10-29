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

import io
import json
import pathlib
import sys
import tempfile
import textwrap
import unittest
from contextlib import redirect_stdout

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
              - id: 'broken_job'
                example_path: 'examples/broken_job'
                entrypoint: 'create.py'
              - id: 'ui_only'
                example_path: ''
                entrypoint: ''
    """
)

_OK_ENTRYPOINT = textwrap.dedent(
    """\
    import helper  # resolved via the example dir on PYTHONPATH
    if __name__ == "__main__":
        raise SystemExit("main must not run during smoke checks")
    """
)
_BROKEN_ENTRYPOINT = "from json import resolve_auth_headers\n"


class ParseEntrypointsTest(unittest.TestCase):
    """Tests for parse_entrypoints."""

    def test_dedupes_and_skips_empty_paths(self) -> None:
        """Repeated entries collapse and UI-only entries are skipped."""
        self.assertEqual(
            smoke.parse_entrypoints(_WORKFLOW),
            [
                smoke.Entrypoint("examples/ok_job", "launch.py"),
                smoke.Entrypoint("examples/broken_job", "create.py"),
            ],
        )

    def test_rejects_example_path_without_entrypoint(self) -> None:
        """Layout drift (entrypoint not adjacent) fails loudly instead of dropping examples."""
        drifted = _WORKFLOW.replace(
            "entrypoint: 'create.py'", "args: ''\n                entrypoint: 'create.py'"
        )
        with self.assertRaisesRegex(ValueError, "examples/broken_job"):
            smoke.parse_entrypoints(drifted)


class SmokeExecutionTest(unittest.TestCase):
    """Tests for smoke_entrypoint and main using real subprocesses."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = pathlib.Path(tmp.name)
        ok_dir = self.repo / "examples" / "ok_job"
        ok_dir.mkdir(parents=True)
        (ok_dir / "launch.py").write_text(_OK_ENTRYPOINT, encoding="utf-8")
        (ok_dir / "helper.py").write_text("", encoding="utf-8")
        broken_dir = self.repo / "examples" / "broken_job"
        broken_dir.mkdir(parents=True)
        (broken_dir / "create.py").write_text(_BROKEN_ENTRYPOINT, encoding="utf-8")
        self.workflow = self.repo / "nightly.yml"
        self.workflow.write_text(_WORKFLOW, encoding="utf-8")
        self.python = pathlib.Path(sys.executable)

    def test_ok_entrypoint_imports_without_running_main(self) -> None:
        """A healthy entrypoint passes and its __main__ block is not executed."""
        result = smoke.smoke_entrypoint(
            self.python, self.repo, smoke.Entrypoint("examples/ok_job", "launch.py")
        )
        self.assertEqual(result, smoke.SmokeResult("examples/ok_job", "launch.py", ok=True))

    def test_import_error_is_reported(self) -> None:
        """An ImportError fails the entrypoint and surfaces the final traceback line."""
        result = smoke.smoke_entrypoint(
            self.python, self.repo, smoke.Entrypoint("examples/broken_job", "create.py")
        )
        self.assertFalse(result.ok)
        self.assertIn("ImportError", result.error)
        self.assertIn("resolve_auth_headers", result.error)

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


if __name__ == "__main__":
    unittest.main()
