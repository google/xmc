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

"""Unit tests for tools.nightly_tests.check_matrix_contract."""

from __future__ import annotations

import argparse
import pathlib
import tempfile
import textwrap
import unittest
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.nightly_tests import check_matrix_contract as cmc
from tools.nightly_tests.workflow_matrix import parse_matrix_items

_VERIFY_STEP = textwrap.dedent(
    """\
          - name: 'Launch'
            run: echo launch
          - name: 'Run Workload Verification Assertions'
            id: verify-example
            env:
              EXPECTED_WORK_UNITS: '${{ matrix.example.expected_work_units }}'
              EXPECTED_DURATION_SEC: '${{ matrix.example.expected_duration_sec }}'
            run: |
              ARGS+=("--expected_work_units=${EXPECTED_WORK_UNITS}")
              ARGS+=("--expected_duration_sec=${EXPECTED_DURATION_SEC}")
              - not-a-step: nested list item inside the run block
          - name: 'Upload'
            run: echo upload
    """
)
_DURATION_FORWARDING = (
    "    EXPECTED_DURATION_SEC: '${{ matrix.example.expected_duration_sec }}'\n",
    '    ARGS+=("--expected_duration_sec=${EXPECTED_DURATION_SEC}")\n',
)
_DURATION_WIRING_VIOLATION = (
    "verify step must forward matrix.example.expected_duration_sec as --expected_duration_sec=."
)


def _workflow(entry: str, verify_step: str = _VERIFY_STEP) -> str:
    """Returns a minimal workflow with the given matrix entry body and verify step."""
    return (
        "jobs:\n  run:\n    strategy:\n      matrix:\n        example:\n"
        + textwrap.indent(textwrap.dedent(entry), " " * 10)
        + "    steps:\n"
        + textwrap.indent(verify_step, " " * 2)
    )


def _audit(workflow_text: str) -> list[str]:
    """Audits `workflow_text` with its parsed matrix items."""
    return cmc.audit_workflow(workflow_text, parse_matrix_items(workflow_text))


def _without_duration_forwarding(step: str) -> str:
    """Returns `step` with the verify step's duration env var and flag removed."""
    for line in _DURATION_FORWARDING:
        step = step.replace(line, "")
    return step


_BASELINE_ENTRY = """\
    - id: 'simple_job'
      args: '--queue=${QUEUE_NAME} --num_work_units=3 --duration=60 --gpus=0'
      expected_work_units: 3
      expected_duration_sec: 60
    - id: 'no_contract_flags'
      args: '--endpoint="localhost:50051" --create-experiments'
"""


class LaunchFlagValuesTest(unittest.TestCase):
    """Tests for extracting contract launch flags from matrix args."""

    def test_extracts_equals_and_space_forms(self) -> None:
        cases: tuple[tuple[str, str, dict[str, str]], ...] = (
            (
                "equals_form",
                "--num_work_units=3 --duration=60",
                {"--num_work_units": "3", "--duration": "60"},
            ),
            ("space_form", "--num_work_units 4 --gpus=0", {"--num_work_units": "4"}),
            ("trailing_flag_without_value", "--duration", {"--duration": ""}),
            ("ignores_prefix_collisions", "--duration_sec=5 --num_work_units_max=2", {}),
            ("empty_args", "", {}),
            ("quoted_multibyte", "--name='débogage ✓' --duration=9", {"--duration": "9"}),
            ("repeated_flag_last_wins", "--duration=60 --duration 90", {"--duration": "90"}),
        )
        for name, args, want in cases:
            with self.subTest(name):
                self.assertEqual(cmc.launch_flag_values(args), want)


class AuditWorkflowTest(unittest.TestCase):
    """Tests for matrix contract auditing against workflow text."""

    def test_baseline_has_no_violations(self) -> None:
        self.assertEqual(_audit(_workflow(_BASELINE_ENTRY)), [])

    def test_reports_entry_violations(self) -> None:
        cases = (
            (
                "mismatched_work_units",
                ("expected_work_units: 3", "expected_work_units: 2"),
                "--num_work_units=3 but expected_work_units=2",
            ),
            (
                "mismatched_duration",
                ("--duration=60", "--duration=180"),
                "--duration=180 but expected_duration_sec=60",
            ),
            (
                "missing_field",
                ("      expected_duration_sec: 60\n", ""),
                "--duration=60 but expected_duration_sec=<unset>",
            ),
            (
                "field_without_arg",
                (" --num_work_units=3", ""),
                "--num_work_units=<unset> but expected_work_units=3",
            ),
            (
                "non_integer_field",
                ("expected_work_units: 3", "expected_work_units: three"),
                "expected_work_units=three",
            ),
            (
                "repeated_flag_last_value_mismatch",
                ("--duration=60", "--duration=60 --duration=61"),
                "--duration=61 but expected_duration_sec=60",
            ),
            ("unbalanced_quote", ("--gpus=0'", "--gpus=\"0'"), "cannot parse args"),
        )
        for name, (old, new), want in cases:
            with self.subTest(name):
                violations = _audit(_workflow(_BASELINE_ENTRY.replace(old, new, 1)))
                self.assertEqual(len(violations), 1, violations)
                self.assertIn("'simple_job'", violations[0])
                self.assertIn(want, violations[0])

    def test_reports_missing_verify_step_forwarding(self) -> None:
        step = _without_duration_forwarding(_VERIFY_STEP)
        self.assertEqual(_audit(_workflow(_BASELINE_ENTRY, step)), [_DURATION_WIRING_VIOLATION])

    def test_ignores_forwarding_tokens_outside_verify_step(self) -> None:
        step = _without_duration_forwarding(_VERIFY_STEP) + textwrap.dedent(
            """\
            - name: 'Other step'
              # matrix.example.expected_duration_sec is forwarded as --expected_duration_sec=
              run: echo other
            """
        )
        self.assertEqual(_audit(_workflow(_BASELINE_ENTRY, step)), [_DURATION_WIRING_VIOLATION])

    def test_reports_missing_verify_step(self) -> None:
        step = _VERIFY_STEP.replace("id: verify-example", "id: something-else")
        self.assertEqual(
            _audit(_workflow(_BASELINE_ENTRY, step)),
            ["verify step (id: verify-example) not found in the nightly workflow."],
        )

    def test_reports_empty_matrix(self) -> None:
        self.assertEqual(_audit("jobs: {}\n"), ["no matrix entries found in the nightly workflow."])


class CheckMatrixContractTest(unittest.TestCase):
    """Tests for the CLI entry function against a repository root."""

    def _run(self, workflow_text: str | None) -> dict[str, object]:
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            if workflow_text is not None:
                path = root / cmc.WORKFLOW_REL
                path.parent.mkdir(parents=True)
                path.write_text(workflow_text, encoding="utf-8")
            return cmc.check_matrix_contract(argparse.Namespace(repo_root=str(root)))

    def test_success_counts_entries(self) -> None:
        self.assertEqual(
            self._run(_workflow(_BASELINE_ENTRY)), {"entries_checked": 2, "violations": 0}
        )

    def test_violations_raise_handled_error_with_details(self) -> None:
        broken = _workflow(_BASELINE_ENTRY.replace("--duration=60", "--duration=61"))
        with self.assertRaises(HandledError) as ctx:
            self._run(broken)
        self.assertEqual(ctx.exception.details, {"violations": [mock.ANY]})

    def test_missing_workflow_raises_handled_error(self) -> None:
        with self.assertRaisesRegex(HandledError, "Workflow not found"):
            self._run(None)

    def test_unreadable_workflow_raises_handled_error(self) -> None:
        with (
            mock.patch.object(pathlib.Path, "read_text", side_effect=PermissionError("denied")),
            self.assertRaisesRegex(HandledError, "Cannot read workflow .*denied"),
        ):
            self._run("placeholder")


if __name__ == "__main__":
    unittest.main()
