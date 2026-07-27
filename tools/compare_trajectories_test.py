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

"""Unit tests for compare_trajectories tool."""

import json
import tempfile
import unittest
from pathlib import Path

from compare_trajectories import generate_markdown_report, parse_transcript


class CompareTrajectoriesTest(unittest.TestCase):
    """Test suite for compare_trajectories module functions."""

    def test_parse_transcript_valid(self):
        with tempfile.NamedTemporaryFile(mode="w", suffix=".jsonl", delete=False) as f:
            f.write(
                json.dumps(
                    {
                        "step_index": 1,
                        "type": "USER_INPUT",
                        "status": "DONE",
                    }
                )
                + "\n"
            )
            f.write(
                json.dumps(
                    {
                        "step_index": 2,
                        "type": "PLANNER_RESPONSE",
                        "status": "DONE",
                        "content": "Step 1 complete",
                        "tool_calls": [
                            {"name": "run_command", "args": {"TargetFile": "/tmp/a.py"}}
                        ],
                    }
                )
                + "\n"
            )
            f.write(
                json.dumps(
                    {
                        "step_index": 3,
                        "type": "PLANNER_RESPONSE",
                        "status": "ERROR",
                        "content": "Step 2 failed",
                        "tool_calls": [{"name": "run_command"}, {"name": "view_file"}],
                    }
                )
                + "\n"
            )
            tmp_path = Path(f.name)

        res = parse_transcript(tmp_path)
        tmp_path.unlink()

        self.assertEqual(res["total_steps"], 3)
        self.assertEqual(res["turns"], 2)
        self.assertEqual(res["error_count"], 1)
        self.assertEqual(res["tool_counts"]["run_command"], 2)
        self.assertEqual(res["tool_counts"]["view_file"], 1)
        self.assertEqual(res["last_response_snippet"], "Step 2 failed")
        self.assertIn("/tmp/a.py", res["modified_files"])

    def test_generate_markdown_report(self):
        baseline = {
            "turns": 5,
            "error_count": 1,
            "total_steps": 10,
            "tool_counts": {"run_command": 3},
            "last_response_snippet": "Baseline done",
        }
        candidate = {
            "turns": 3,
            "error_count": 0,
            "total_steps": 6,
            "tool_counts": {"run_command": 2},
            "last_response_snippet": "Candidate done",
        }

        report = generate_markdown_report(baseline, candidate)
        self.assertIn("fewer turns", report)
        self.assertIn("Pillar 1: Trajectory Telemetry", report)
        self.assertIn("Pillar 2: Outcome & Quality Evaluation", report)
        self.assertIn("Candidate done", report)


if __name__ == "__main__":
    unittest.main()
