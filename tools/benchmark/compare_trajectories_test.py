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


# Sanitized fixtures mirroring the two transcript.jsonl schemas with synthetic values only. Both
# describe the same trajectory: two file edits to the same two files and two error steps.
_LEGACY_SCHEMA_STEPS: tuple[dict, ...] = (
    {"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE"},
    {
        "step_index": 1,
        "type": "PLANNER_RESPONSE",
        "status": "DONE",
        "content": "Editing files",
        "tool_calls": [
            {"name": "write_to_file", "args": {"TargetFile": "/ws/a.py", "CodeContent": "x"}},
            {
                "name": "replace_file_content",
                "args": {"TargetFile": "/ws/b.py", "TargetContent": "x", "ReplacementContent": "y"},
            },
        ],
    },
    {"step_index": 2, "type": "GENERIC", "status": "ERROR", "content": "Step failed"},
    {"step_index": 3, "type": "GENERIC", "status": "ERROR", "content": "Step failed"},
    {"step_index": 4, "type": "PLANNER_RESPONSE", "status": "DONE", "content": "Done ✅"},
)

_CURRENT_SCHEMA_STEPS: tuple[dict, ...] = (
    {"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE"},
    {
        "step_index": 1,
        "source": "MODEL",
        "type": "PLANNER_RESPONSE",
        "status": "DONE",
        "content": "Editing files",
        "tool_calls": [
            {"name": "write_file", "args": {"Path": "/ws/a.py", "Content": "x"}},
            {
                "name": "edit_file",
                "args": {"Path": "/ws/b.py", "Target": "x", "Replacement": "y"},
            },
        ],
    },
    {
        "step_index": 2,
        "source": "MODEL",
        "type": "GENERIC",
        "status": "ERROR",
        "error": "file does not exist",
        "content": "Encountered error in step execution: file does not exist",
    },
    {
        "step_index": 3,
        "source": "SYSTEM",
        "type": "ERROR_MESSAGE",
        "status": "DONE",
        "error": "API error (attempt 1): RESOURCE_EXHAUSTED",
    },
    {"step_index": 4, "type": "PLANNER_RESPONSE", "status": "DONE", "content": "Done ✅"},
)


def _write_transcript(directory: str, name: str, lines: list[str]) -> Path:
    """Writes raw JSONL lines to a transcript file and returns its path."""
    path = Path(directory) / name
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return path


def _encode_steps(steps: tuple[dict, ...]) -> list[str]:
    """Serializes transcript steps into JSONL lines."""
    return [json.dumps(step, ensure_ascii=False) for step in steps]


class TranscriptSchemaCompatibilityTest(unittest.TestCase):
    """Verifies parse_transcript handles both the legacy and the current transcript schema."""

    def _parse(self, lines: list[str]) -> dict:
        with tempfile.TemporaryDirectory() as tmp_dir:
            return parse_transcript(_write_transcript(tmp_dir, "transcript.jsonl", lines))

    def test_legacy_and_current_schema_yield_identical_metrics(self):
        legacy = self._parse(_encode_steps(_LEGACY_SCHEMA_STEPS))
        current = self._parse(_encode_steps(_CURRENT_SCHEMA_STEPS))

        for key in ("total_steps", "turns", "error_count", "modified_files", "last_response"):
            with self.subTest(metric=key):
                self.assertEqual(legacy[key], current[key])
        self.assertEqual(sum(legacy["tool_counts"].values()), sum(current["tool_counts"].values()))

    def test_current_schema_counts_write_file_and_edit_file(self):
        res = self._parse(_encode_steps(_CURRENT_SCHEMA_STEPS))

        self.assertEqual(res["modified_files"], ["/ws/a.py", "/ws/b.py"])
        self.assertEqual(res["tool_counts"]["write_file"], 1)
        self.assertEqual(res["tool_counts"]["edit_file"], 1)

    def test_error_steps_counted_in_both_schemas(self):
        cases = {
            "legacy_status": ({"type": "GENERIC", "status": "ERROR"}, 1),
            "current_error_field": ({"type": "GENERIC", "status": "INVALID", "error": "boom"}, 1),
            "current_error_message_type": ({"type": "ERROR_MESSAGE", "status": "DONE"}, 1),
            "empty_error_field": ({"type": "GENERIC", "status": "DONE", "error": ""}, 0),
            "success": ({"type": "GENERIC", "status": "DONE"}, 0),
        }
        for label, (step, expected) in cases.items():
            with self.subTest(case=label):
                self.assertEqual(self._parse([json.dumps(step)])["error_count"], expected)

    def test_mutating_call_without_path_arg_is_ignored(self):
        step = {"type": "PLANNER_RESPONSE", "tool_calls": [{"name": "edit_file", "args": {}}]}

        res = self._parse([json.dumps(step)])

        self.assertEqual(res["modified_files"], [])
        self.assertEqual(res["tool_counts"]["edit_file"], 1)

    def test_malformed_lines_are_skipped_in_current_schema(self):
        lines = ["", "not json {", "[1, 2]", "   "] + _encode_steps(_CURRENT_SCHEMA_STEPS)

        res = self._parse(lines)

        self.assertEqual(res["total_steps"], len(_CURRENT_SCHEMA_STEPS))
        self.assertEqual(res["error_count"], 2)
        self.assertEqual(res["last_response"], "Done ✅")


if __name__ == "__main__":
    unittest.main()
