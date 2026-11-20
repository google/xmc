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

"""Unit tests for tools.lint.check_workflow_inputs."""

from __future__ import annotations

import argparse
import io
import json
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.lint import check_workflow_inputs as cli

_JOBS = """
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: step
        if: ${{ inputs.dry_run == false }}
        run: echo hi
"""


def _workflow(on_block: str, jobs: str = _JOBS) -> str:
    """Builds a workflow from a dedented `on:` block and jobs block."""
    return textwrap.dedent(on_block).strip("\n") + "\n" + textwrap.dedent(jobs).lstrip("\n")


def _matches(text: str) -> list[tuple[int, str]]:
    """Returns (line, match) pairs reported for text."""
    return [(v["line"], v["match"]) for v in cli.find_violations(text, "wf.yml")]


class ParseTriggersTest(unittest.TestCase):
    """Trigger extraction across `on:` string, list and mapping forms."""

    def test_parses_trigger_forms(self) -> None:
        cases = {
            "scalar": ("on: push", {"push"}),
            "quoted_scalar": ("on: 'merge_group'", {"merge_group"}),
            "quoted_key": ('"on": schedule', {"schedule"}),
            "flow_list": ("on: [push, 'pull_request', ]", {"push", "pull_request"}),
            "flow_mapping": (
                "on: {push: {branches: [main, dev]}, workflow_call: }",
                {"push", "workflow_call"},
            ),
            "block_list": (
                "on:\n  - pull_request_target\n  - workflow_dispatch",
                {"pull_request_target", "workflow_dispatch"},
            ),
            "block_list_at_root_indent": ("on:\n- push\n- schedule", {"push", "schedule"}),
            "block_mapping": (
                "on:  # triggers\n  push:\n    branches:\n      - main\n\n"
                "  # comment\n  workflow_call:\n    inputs: {}\njobs: {}",
                {"push", "workflow_call"},
            ),
        }
        for name, (text, expected) in cases.items():
            with self.subTest(name=name):
                self.assertEqual(cli.parse_triggers(text, "wf.yml"), expected)

    def test_unquoted_on_key_is_not_read_as_boolean(self) -> None:
        # YAML 1.1 loaders parse a bare `on` key as True; the text parser must still find it.
        self.assertEqual(cli.parse_triggers("name: x\non:\n  push:\n", "wf.yml"), {"push"})

    def test_malformed_workflows_raise_handled_error(self) -> None:
        cases = {
            "tab_indent": "on:\n\tpush:\n",
            "no_on_key": "name: x\njobs: {}\n",
            "nested_on_only": "jobs:\n  on: push\n",
            "unterminated_flow": "on: [push, pull_request\n",
            "empty_block": "on:\njobs: {}\n",
        }
        for name, text in cases.items():
            with self.subTest(name=name), self.assertRaises(HandledError) as ctx:
                cli.parse_triggers(text, "wf.yml")
            self.assertIn("wf.yml", ctx.exception.message)
            self.assertIn("actionlint", ctx.exception.instruction)


class FindViolationsTest(unittest.TestCase):
    """Predicate coverage across positive and negative boundary sets."""

    def test_flags_every_inputless_trigger(self) -> None:
        for event in sorted(cli.INPUTLESS_EVENTS):
            for name, on_block in {
                "scalar": f"on: {event}",
                "list": f"on: [{event}, workflow_call]",
                "mapping": f"on:\n  {event}:\n  workflow_dispatch:",
            }.items():
                with self.subTest(event=event, form=name):
                    if_line = 7 + on_block.count("\n")
                    self.assertEqual(
                        _matches(_workflow(on_block)), [(if_line, "inputs.dry_run == false")]
                    )

    def test_ignores_call_and_dispatch_only_workflows(self) -> None:
        for on_block in ("on: workflow_call", "on: [workflow_dispatch, workflow_call]"):
            with self.subTest(on_block=on_block):
                self.assertEqual(_matches(_workflow(on_block)), [])

    def test_flags_unsafe_comparisons(self) -> None:
        cases = {
            "eq_false": ("${{ inputs.a == false }}", ["inputs.a == false"]),
            "ne_false": ("${{ inputs.a != false }}", ["inputs.a != false"]),
            "eq_true": ("${{ inputs.a==true }}", ["inputs.a==true"]),
            "ne_true_hyphen": ("${{ inputs.cache-x  !=  true }}", ["inputs.cache-x  !=  true"]),
            "reversed": ("${{ false == inputs.a }}", ["false == inputs.a"]),
            "reversed_ne": ("${{ (true != inputs.a) }}", ["true != inputs.a"]),
            "event_inputs": (
                "${{ github.event.inputs.a == false }}",
                ["github.event.inputs.a == false"],
            ),
            "reversed_event_inputs": (
                "${{ true == github.event.inputs.a }}",
                ["true == github.event.inputs.a"],
            ),
            "multiple_per_line": (
                "${{ inputs.a == false && 'x' || 'y' }} ${{ inputs.b != true }}",
                ["inputs.a == false", "inputs.b != true"],
            ),
            "origin_incident": (
                "${{ inputs.cache-test-results == false && 'false' || 'true' }}",
                ["inputs.cache-test-results == false"],
            ),
        }
        for name, (expr, expected) in cases.items():
            text = f"on: pull_request\njobs:\n  a:\n    env:\n      X: {expr}\n"
            with self.subTest(name=name):
                self.assertEqual(_matches(text), [(5, m) for m in expected])

    def test_ignores_safe_patterns(self) -> None:
        safe = (
            "${{ format('{0}', inputs.a) == 'false' }}",
            "${{ inputs.a == 'false' }}",
            "${{ 'true' == inputs.a }}",
            "${{ steps.x.outputs.y == 'true' }}",
            "${{ needs.x.outputs.inputs.a == false }}",
            "${{ !inputs.a }}",
            "${{ inputs.a || 'default' }}",
            "${{ inputs.a == falsey }}",
            "inputs.a == false  # outside any expression",
        )
        for expr in safe:
            with self.subTest(expr=expr):
                text = f"on: push\njobs:\n  a:\n    env:\n      X: {expr}\n"
                self.assertEqual(_matches(text), [])

    def test_flags_if_values_without_expression_braces(self) -> None:
        text = _workflow(
            "on: merge_group",
            """
            jobs:
              a:
                if: inputs.a != false
                steps:
                  - if: true == inputs.b
                    run: echo
                  - if: >-
                      github.event_name == 'push' &&
                      inputs.c == true
                    run: echo
                  - if: ${{ inputs.d == false }}
                    run: echo
            """,
        )
        self.assertEqual(
            _matches(text),
            [
                (4, "inputs.a != false"),
                (6, "true == inputs.b"),
                (10, "inputs.c == true"),
                (12, "inputs.d == false"),
            ],
        )

    def test_flags_block_scalar_if_at_end_of_file(self) -> None:
        text = "on: push\njobs:\n  a:\n    if: |\n      inputs.a == false"
        self.assertEqual(_matches(text), [(5, "inputs.a == false")])

    def test_ignores_commented_out_expressions_and_run_keys(self) -> None:
        text = _workflow(
            "on: push",
            """
            jobs:
              a:
                steps:
                  # - if: ${{ inputs.a == false }}
                  #   if: inputs.a == false
                  - if: success()
                    run: test inputs.a == false
            """,
        )
        self.assertEqual(_matches(text), [])

    def test_remediation_names_input_and_literal(self) -> None:
        text = "on: push\njobs:\n  a:\n    if: ${{ inputs.true_flag == false }}\n"
        (violation,) = cli.find_violations(text, "wf.yml")
        self.assertIn("format('{0}', inputs.true_flag) == 'false'", violation["remediation"])
        self.assertIn("inputs.true_flag || <default>", violation["remediation"])
        self.assertEqual(violation["path"], "wf.yml")


class CheckWorkflowInputsTest(unittest.TestCase):
    """End-to-end coverage of file discovery, CLI output and exit codes."""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.workflows = self.root / ".github/workflows"
        self.workflows.mkdir(parents=True)

    def _args(self, *paths: str) -> argparse.Namespace:
        return argparse.Namespace(repo_root=str(self.root), paths=list(paths))

    def test_empty_workflow_dir_passes(self) -> None:
        self.assertEqual(
            cli.check_workflow_inputs(self._args()), {"files_scanned": 0, "violations": 0}
        )

    def test_scans_yml_and_yaml_and_reports_relative_paths(self) -> None:
        (self.workflows / "ok.yaml").write_text(_workflow("on: workflow_call"))
        (self.workflows / "bad.yml").write_text(_workflow("on: pull_request"))
        (self.root / ".github/actions").mkdir()
        (self.root / ".github/actions/action.yml").write_text(_workflow("on: push"))
        with self.assertRaises(HandledError) as ctx:
            cli.check_workflow_inputs(self._args())
        assert ctx.exception.details is not None
        self.assertEqual(
            [(v["path"], v["line"]) for v in ctx.exception.details["violations"]],
            [(".github/workflows/bad.yml", 7)],
        )

    def test_explicit_paths_outside_repo_root(self) -> None:
        with tempfile.TemporaryDirectory() as other:
            outside = Path(other) / "copy.yml"
            outside.write_text(_workflow("on: workflow_dispatch"))
            result = cli.check_workflow_inputs(self._args(str(outside)))
            self.assertEqual(result, {"files_scanned": 1, "violations": 0})
            self.assertEqual(cli._display_path(outside, self.root.resolve()), str(outside))

    def test_unreadable_workflows_raise_handled_error(self) -> None:
        (self.workflows / "binary.yml").write_bytes(b"on: push\n\xff\xfe\n")
        for paths in ((), (str(self.root / "missing.yml"),)):
            with self.subTest(paths=paths), self.assertRaises(HandledError) as ctx:
                cli.check_workflow_inputs(self._args(*paths))
            self.assertIn("Cannot read workflow", ctx.exception.message)

    def _run_main(self, *argv: str) -> tuple[int, dict]:
        out, err = io.StringIO(), io.StringIO()
        with (
            mock.patch.dict("os.environ", {"XMC_BYPASS_VENV": "1"}),
            mock.patch(
                "sys.argv", ["check_workflow_inputs.py", "--repo-root", str(self.root), *argv]
            ),
            mock.patch("sys.stdout", out),
            mock.patch("sys.stderr", err),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            cli.main()
        code = exit_ctx.exception.code
        return int(code or 0), json.loads(out.getvalue() or err.getvalue())

    def test_main_reports_violations_with_exit_one(self) -> None:
        (self.workflows / "bad.yml").write_text(_workflow("on: [schedule]"))
        code, payload = self._run_main()
        self.assertEqual(code, 1)
        self.assertEqual(payload["error_type"], "handled")
        self.assertIn("format('{0}', inputs.<name>)", payload["instruction"])
        self.assertEqual(payload["details"]["violations"][0]["match"], "inputs.dry_run == false")

    def test_main_malformed_yaml_exits_one_with_instruction(self) -> None:
        (self.workflows / "bad.yml").write_text("name: x\njobs: {}\n")
        code, payload = self._run_main()
        self.assertEqual(code, 1)
        self.assertIn("no top-level `on:` key", payload["message"])
        self.assertIn("instruction", payload)

    def test_main_success_exits_zero(self) -> None:
        (self.workflows / "ok.yml").write_text(_workflow("on: workflow_call"))
        code, payload = self._run_main()
        self.assertEqual(code, 0)
        self.assertEqual(payload["result"], {"files_scanned": 1, "violations": 0})


if __name__ == "__main__":
    unittest.main()
