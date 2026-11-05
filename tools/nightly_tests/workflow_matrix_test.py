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

"""Unit tests for tools.nightly_tests.workflow_matrix."""

from __future__ import annotations

import os
import pathlib
import tempfile
import textwrap
import unittest

from tools.nightly_tests import workflow_matrix

_WORKFLOW_SAMPLE = textwrap.dedent(
    """\
    jobs:
      run-and-verify-examples:
        strategy:
          matrix:
            example:
              - id: 'ok_job'
                name: "OK Job"
                example_path: 'examples/ok_job'
                entrypoint: 'launch.py'  # inline comment
              - id: "ui_only"
                name: 'UI Only'
                example_path: ''
                entrypoint: 'unterminated
        steps:
          - name: 'Step sharing matrix key'
            with:
              example_path: 'ignored/outside/matrix'
    """
)


class WorkflowMatrixTest(unittest.TestCase):
    """Tests for workflow YAML matrix parsing, path resolution, and filter validation."""

    def test_parse_matrix_items_scopes_to_list_items_and_unquotes(self) -> None:
        workflow = textwrap.dedent(
            """\
            jobs:
              a:
                strategy:
                  matrix:
                    example:
                      - id: 'first'
                        name: "First Example"
                        example_path: examples/a  # comment stripped
                        entrypoint: 'launch.py'
                    id: 'not_an_item_key'
              b:
                strategy:
                  matrix:
                    id: 'stray'
                    entrypoint: 'stray.py'
                    example:
                      - id: "second"
                        entrypoint: 'unterminated
            """
        )
        self.assertEqual(
            workflow_matrix.parse_matrix_items(workflow),
            [
                {
                    "id": "first",
                    "name": "First Example",
                    "example_path": "examples/a",
                    "entrypoint": "launch.py",
                },
                {"id": "second", "entrypoint": "unterminated"},
            ],
        )

    def test_resolve_workflow_path_handles_repo_dir_cwd_and_default(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            tmp_dir = pathlib.Path(tmp_str)
            explicit = workflow_matrix.resolve_workflow_path(tmp_dir)
            self.assertEqual(explicit, tmp_dir / workflow_matrix.WORKFLOW_REL)

            prev_cwd = pathlib.Path.cwd()
            self.addCleanup(os.chdir, prev_cwd)
            os.chdir(tmp_dir)
            self.assertEqual(
                workflow_matrix.resolve_workflow_path(), workflow_matrix.DEFAULT_WORKFLOW
            )

            local_wf = tmp_dir / workflow_matrix.WORKFLOW_REL
            local_wf.parent.mkdir(parents=True)
            local_wf.write_text("jobs: {}\n", encoding="utf-8")
            self.assertEqual(workflow_matrix.resolve_workflow_path(), workflow_matrix.WORKFLOW_REL)

    def test_validate_examples_filter_accepts_valid_ids_and_rejects_malformed(self) -> None:
        workflow_matrix.validate_examples_filter("", _WORKFLOW_SAMPLE)
        workflow_matrix.validate_examples_filter("OK_JOB,ui_only", _WORKFLOW_SAMPLE)
        for invalid in (
            "nonexistent_module_test",
            "ok_job, ui_only",
            "ok_job,",
            ",ok_job",
            "ok_job,,ui_only",
            ",",
            " ",
        ):
            with self.subTest(invalid=invalid), self.assertRaisesRegex(ValueError, "ok_job"):
                workflow_matrix.validate_examples_filter(invalid, _WORKFLOW_SAMPLE)
        with self.assertRaisesRegex(ValueError, "no matrix.example.id"):
            workflow_matrix.validate_examples_filter("ok_job", "jobs: {}\n")


if __name__ == "__main__":
    unittest.main()
