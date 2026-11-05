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

import os
import pathlib
import tempfile
import textwrap
import unittest

from tools.nightly_tests import workflow_matrix as wm

_WORKFLOW_SAMPLE = textwrap.dedent(
    """\
    jobs:
      a:
        strategy:
          matrix:
            example:
              - id: 'ok_job'
                name: "OK Job"
                example_path: examples/ok_job  # inline comment
                entrypoint: 'launch.py'
            id: 'not_an_item_key'
        steps:
          - name: 'Step sharing matrix key'
            with:
              example_path: 'ignored/outside/matrix'
      b:
        strategy:
          matrix:
            id: 'stray'
            entrypoint: 'stray.py'
            example:
              - id: "ui_only"
                entrypoint: 'unterminated
    """
)


class WorkflowMatrixTest(unittest.TestCase):
    """Tests for workflow YAML matrix parsing, path resolution, and filter validation."""

    def test_parse_matrix_items_scopes_to_list_items_and_unquotes(self) -> None:
        expected_first = {
            "id": "ok_job",
            "name": "OK Job",
            "example_path": "examples/ok_job",
            "entrypoint": "launch.py",
        }
        self.assertEqual(
            wm.parse_matrix_items(_WORKFLOW_SAMPLE),
            [expected_first, {"id": "ui_only", "entrypoint": "unterminated"}],
        )

    def test_resolve_workflow_path_handles_repo_dir_cwd_and_default(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            tmp_dir = pathlib.Path(tmp_str)
            self.assertEqual(wm.resolve_workflow_path(tmp_dir), tmp_dir / wm.WORKFLOW_REL)
            self.addCleanup(os.chdir, pathlib.Path.cwd())
            os.chdir(tmp_dir)
            self.assertEqual(wm.resolve_workflow_path(), wm.DEFAULT_WORKFLOW)
            local_wf = tmp_dir / wm.WORKFLOW_REL
            local_wf.parent.mkdir(parents=True)
            local_wf.write_text("jobs: {}\n", encoding="utf-8")
            self.assertEqual(wm.resolve_workflow_path(), wm.WORKFLOW_REL)

    def test_validate_examples_filter_accepts_valid_ids_and_rejects_malformed(self) -> None:
        wm.validate_examples_filter("", _WORKFLOW_SAMPLE)
        wm.validate_examples_filter("OK_JOB,ui_only", _WORKFLOW_SAMPLE)
        for invalid in (
            "bad",
            "ok_job, ui_only",
            "ok_job,",
            ",ok_job",
            "ok_job,,ui_only",
            ",",
            " ",
        ):
            with self.subTest(invalid=invalid), self.assertRaisesRegex(ValueError, "ok_job"):
                wm.validate_examples_filter(invalid, _WORKFLOW_SAMPLE)
        with self.assertRaisesRegex(ValueError, "no matrix.example.id"):
            wm.validate_examples_filter("ok_job", "jobs: {}\n")


if __name__ == "__main__":
    unittest.main()
