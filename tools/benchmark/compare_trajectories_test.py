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

import argparse
import json
import tempfile
import unittest
from pathlib import Path

from tools.benchmark.compare_trajectories import (
    compare_trajectories,
    generate_markdown_report,
    parse_transcript,
)


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
                            {"name": "write_to_file", "args": {"TargetFile": "/tmp/a.py"}}
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
        self.assertGreater(res["file_bytes"], 0)
        self.assertGreaterEqual(res["est_tokens"], 0)
        self.assertEqual(res["tool_counts"]["write_to_file"], 1)
        self.assertEqual(res["tool_counts"]["run_command"], 1)

        self.assertEqual(res["tool_counts"]["view_file"], 1)
        self.assertEqual(res["last_response_snippet"], "Step 2 failed")
        self.assertIn("/tmp/a.py", res["modified_files"])

    def test_generate_markdown_report(self):
        baseline = {
            "turns": 5,
            "file_bytes": 10240,
            "est_tokens": 2560,
            "error_count": 1,
            "total_steps": 10,
            "tool_counts": {"run_command": 3},
            "last_response_snippet": "Baseline done",
        }
        candidate = {
            "turns": 3,
            "file_bytes": 8192,
            "est_tokens": 2048,
            "error_count": 0,
            "total_steps": 6,
            "tool_counts": {"run_command": 2},
            "last_response_snippet": "Candidate done",
        }

        report = generate_markdown_report(baseline, candidate, prompt="Test prompt text")
        self.assertIn("Test prompt text", report)
        self.assertIn("Context Footprint", report)
        self.assertIn("fewer turns", report)
        self.assertIn("Pillar 1: Trajectory Telemetry", report)
        self.assertIn("Pillar 2: Outcome & Quality Evaluation", report)
        self.assertIn("Final Benchmark Verdict", report)

    def test_supplied_verdict_section_replaces_the_placeholder(self):
        baseline = {
            "turns": 5,
            "file_bytes": 10240,
            "est_tokens": 2560,
            "error_count": 1,
            "total_steps": 10,
            "tool_counts": {"run_command": 3},
            "last_response_snippet": "Baseline done",
        }
        candidate = dict(baseline, last_response_snippet="Candidate done")

        report = generate_markdown_report(
            baseline, candidate, verdict_section="### Derived Verdict\n\n**SHIP**\n"
        )
        self.assertIn("**SHIP**", report)
        self.assertNotIn("Final Benchmark Verdict", report)

    def test_fallback_placeholder_lists_all_ship_variants(self):
        baseline = {
            "turns": 2,
            "file_bytes": 4096,
            "est_tokens": 1024,
            "error_count": 0,
            "total_steps": 4,
            "tool_counts": {},
            "last_response_snippet": "Baseline",
        }
        report = generate_markdown_report(baseline, baseline)
        self.assertIn("SHIP (NO NEGATIVE REGRESSION SCENARIO)", report)
        self.assertIn("SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED)", report)

    def test_compare_trajectories_with_assertions_flag(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            b_path = Path(tmp_dir) / "baseline.jsonl"
            c_path = Path(tmp_dir) / "candidate.jsonl"
            entry = (
                json.dumps({"type": "PLANNER_RESPONSE", "status": "DONE", "content": "Done"}) + "\n"
            )
            b_path.write_text(entry, encoding="utf-8")
            c_path.write_text(entry, encoding="utf-8")
            (Path(tmp_dir) / "target.py").write_text("fixed\n", encoding="utf-8")

            spec_path = Path(tmp_dir) / "assertions.json"
            spec_path.write_text(
                json.dumps(
                    {
                        "origin": {
                            "assertions": [
                                {"kind": "file_contains", "path": "target.py", "pattern": "^fixed$"}
                            ]
                        }
                    }
                ),
                encoding="utf-8",
            )
            args = argparse.Namespace(
                baseline_transcript=str(b_path),
                candidate_transcript=str(c_path),
                prompt="Fix bug",
                assertions=str(spec_path),
                output=None,
                origin_workspace=tmp_dir,
                origin_baseline_workspace=None,
                transfer_workspace=None,
                negative_workspace=None,
            )
            res = compare_trajectories(args)
            self.assertEqual(res["verdict"], "SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED)")
            self.assertIn("Pre-Registered Scenario Verdict", res["markdown_report"])


if __name__ == "__main__":
    unittest.main()
