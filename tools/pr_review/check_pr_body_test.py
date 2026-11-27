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

"""Unit tests for tools.pr_review.check_pr_body."""

from __future__ import annotations

import argparse
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.pr_review import check_pr_body as cli

_VALID_ASSUMPTION = "- Cache is warm — CI seeds it nightly — first run is slower"


def _body(assumptions: str = _VALID_ASSUMPTION, *, omit: str = "", tag: bool = True) -> str:
    """Builds a template-conformant PR body, optionally omitting one section."""
    sections = {
        "Summary": "Adds a thing.",
        "Motivation & Context": "Because.",
        "Key Changes": "- Change one\n  - nested detail",
        "Assumptions": assumptions,
        "Verification & Testing": "```bash\n## not a heading\nbazel test //...\n```",
        "Related Issues / Bugs": "N/A",
    }
    parts = [f"## {name}\n{text}\n" for name, text in sections.items() if name != omit]
    trailer = "TAG=agy\nCONV=abc\n" if tag else "CONV=abc\n"
    return "\n".join(parts) + "\n" + trailer


def _violations(text: str) -> list[dict]:
    """Returns the violations raised for text, or [] when it passes."""
    try:
        cli.check_body(text)
    except HandledError as e:
        assert e.details is not None
        return list(e.details["violations"])
    return []


class AssumptionViolationTest(unittest.TestCase):
    """Bullet-format predicate across positive and negative boundary sets."""

    def test_classifies_bullets(self) -> None:
        cases = {
            "valid": ("a — b — c", None),
            "four_parts": ("a — b — c — d", None),
            "none_exact": (cli.NONE_BULLET, None),
            "code_span_dash_plus_two": ("`x — y` holds — why — impact", None),
            "none_without_period": ("None — requirements were fully specified", "1 ' — '"),
            "one_separator": ("a — b", "1 ' — '"),
            "no_separator": ("Plain prose assumption.", "0 ' — '"),
            "dash_only_in_code_span": ("`a — b — c` only", "0 ' — '"),
            "double_backtick_span": ("``a — b`` — why", "1 ' — '"),
            "hyphen_not_em_dash": ("a - b - c", "0 ' — '"),
            "no_spaces_around_dash": ("a—b—c", "0 ' — '"),
            "empty_middle": ("a —  — c", "empty part"),
            "empty_last": ("a — b — ", "empty part"),
        }
        for name, (text, expected) in cases.items():
            with self.subTest(name=name):
                reason = cli.assumption_violation(text)
                if expected is None:
                    self.assertIsNone(reason)
                else:
                    self.assertIn(expected, reason or "")


class CheckBodyTest(unittest.TestCase):
    """End-to-end body validation."""

    def test_valid_body_passes(self) -> None:
        result = cli.check_body(_body())
        self.assertEqual(result["sections"], list(cli.REQUIRED_SECTIONS))
        self.assertEqual(result["assumption_count"], 1)

    def test_each_missing_section_is_reported(self) -> None:
        for name in cli.REQUIRED_SECTIONS:
            with self.subTest(section=name):
                violations = _violations(_body(omit=name))
                self.assertIn(
                    {"line": None, "text": f"## {name}", "reason": "missing required section"},
                    violations,
                )

    def test_missing_tag_line_is_reported(self) -> None:
        self.assertEqual(
            _violations(_body(tag=False)),
            [{"line": None, "text": "TAG=agy", "reason": "missing TAG=agy line"}],
        )

    def test_none_forms(self) -> None:
        for name, assumptions in {
            "bullet": f"- {cli.NONE_BULLET}",
            "paragraph": cli.NONE_BULLET,
        }.items():
            with self.subTest(form=name):
                self.assertEqual(cli.check_body(_body(assumptions))["assumption_count"], 0)

    def test_malformed_bullets_report_line_and_text(self) -> None:
        text = _body("Intro paragraph.\n- a — b\n* x — y — z\n+ prose only")
        self.assertEqual(
            [(v["line"], v["text"]) for v in _violations(text)],
            [(13, "a — b"), (15, "prose only")],
        )

    def test_nested_bullets_and_wrapped_lines(self) -> None:
        assumptions = (
            "- Wrapped assumption —\n  why it holds — impact\n"
            "  - nested bullet without separators\n"
            "    continuation of nested bullet\n"
            "- second — why — impact"
        )
        result = cli.check_body(_body(assumptions))
        self.assertEqual(result["assumption_count"], 2)

    def test_fenced_code_in_assumptions_is_ignored(self) -> None:
        assumptions = f"{_VALID_ASSUMPTION}\n~~~~text\n- not a bullet\n~~~\n## nor a heading\n~~~~"
        self.assertEqual(cli.check_body(_body(assumptions))["assumption_count"], 1)

    def test_crlf_line_endings(self) -> None:
        text = _body("- a — b — c\n- bad").replace("\n", "\r\n")
        self.assertEqual([v["text"] for v in _violations(text)], ["bad"])

    def test_empty_assumptions_section(self) -> None:
        self.assertEqual(
            _violations(_body("")),
            [{"line": None, "text": "## Assumptions", "reason": "section has no bullets"}],
        )

    def test_tag_line_and_h1_end_sections(self) -> None:
        text = _body() + "\n- trailer bullet after TAG\n# Title\n- after h1"
        self.assertEqual(_violations(text), [])

    def test_level_three_heading_stays_in_section(self) -> None:
        text = _body(f"### Details\n{_VALID_ASSUMPTION}\n- bad")
        self.assertEqual([v["text"] for v in _violations(text)], ["bad"])

    def test_none_line_without_blank_line_does_not_extend_previous_bullet(self) -> None:
        text = _body(f"- incomplete — missing impact\n{cli.NONE_BULLET}")
        self.assertEqual([v["text"] for v in _violations(text)], ["incomplete — missing impact"])


class CliTest(unittest.TestCase):
    """CLI input handling, JSON output and exit codes."""

    def _run(self, *argv: str, stdin: str = "") -> tuple[int, dict]:
        out, err = io.StringIO(), io.StringIO()
        with (
            mock.patch.dict("os.environ", {"XMC_BYPASS_VENV": "1"}),
            mock.patch("sys.argv", ["check_pr_body.py", *argv]),
            mock.patch("sys.stdin", io.StringIO(stdin)),
            mock.patch("sys.stdout", out),
            mock.patch("sys.stderr", err),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            cli.main()
        return int(exit_ctx.exception.code or 0), json.loads(out.getvalue() or err.getvalue())

    def test_body_file_success(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "pr_body.md"
            path.write_text(_body(), encoding="utf-8")
            code, payload = self._run("--body-file", str(path))
        self.assertEqual(code, 0)
        self.assertEqual(payload["status"], "success")
        self.assertEqual(payload["result"]["assumption_count"], 1)

    def test_stdin_violations_exit_one_with_instruction(self) -> None:
        code, payload = self._run("--body-file", "-", stdin=_body("- a — b"))
        self.assertEqual(code, 1)
        self.assertEqual(payload["error_type"], "handled")
        self.assertIn("<impact if", payload["instruction"])
        self.assertEqual(payload["details"]["violations"][0]["line"], 12)

    def test_missing_file_is_handled_error(self) -> None:
        with self.assertRaises(HandledError) as ctx:
            cli.check_pr_body(argparse.Namespace(body_file="/nonexistent/pr_body.md"))
        self.assertIn("Cannot read PR body file", ctx.exception.message)
        self.assertIn("--body-file", ctx.exception.instruction)


if __name__ == "__main__":
    unittest.main()
