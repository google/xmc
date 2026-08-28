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

"""Unit tests for analyze_diff_and_synthesize tool."""

import argparse
import unittest
from unittest.mock import MagicMock, patch

from tools.benchmark.analyze_diff_and_synthesize import (
    analyze_diff_and_synthesize,
    extract_diff_structure,
    get_git_diff,
)
from tools.common.tool_utils import HandledError


class AnalyzeDiffAndSynthesizeTest(unittest.TestCase):
    """Test suite for analyze_diff_and_synthesize module functions."""

    def test_extract_diff_structure_skill(self):
        changed_files = [".agents/skills/xmc_create_pr/SKILL.md"]
        diff_text = "+ Use gh pr create to submit pull requests\n- Old line"

        res = extract_diff_structure(changed_files, diff_text)
        self.assertEqual(res["category"], "skill")
        self.assertIn("Use gh pr create to submit pull requests", res["added_lines"])
        self.assertIn("Old line", res["removed_lines"])

    def test_extract_diff_structure_rule(self):
        changed_files = [".agents/rules/languages/golang.md"]
        diff_text = "+ Enforce error wrapping with %w in Go functions"

        res = extract_diff_structure(changed_files, diff_text)
        self.assertEqual(res["category"], "rule")
        self.assertIn("Enforce error wrapping with %w in Go functions", res["added_lines"])

    @patch("subprocess.run")
    def test_get_git_diff_success(self, mock_run):
        mock_diff = MagicMock()
        mock_diff.stdout = "+ test diff line"
        mock_name = MagicMock()
        mock_name.stdout = ".agents/rules/test.md\n"

        mock_run.side_effect = [mock_diff, mock_name]

        diff_text, changed_files = get_git_diff(".agents", "HEAD")
        self.assertIn("+ test diff line", diff_text)
        self.assertEqual(changed_files, [".agents/rules/test.md"])

    @patch("subprocess.run")
    def test_analyze_diff_untracked_files_error(self, mock_run):
        mock_diff = MagicMock()
        mock_diff.stdout = ""
        mock_name = MagicMock()
        mock_name.stdout = ""
        mock_untracked = MagicMock()
        mock_untracked.stdout = ".agents/skills/new_skill/SKILL.md\n"

        mock_run.side_effect = [mock_diff, mock_name, mock_untracked]

        args = argparse.Namespace(target_dir=".agents", diff_spec=None)
        with self.assertRaises(HandledError) as ctx:
            analyze_diff_and_synthesize(args)

        self.assertIn("Untracked files detected", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
