#!/usr/bin/env python3
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

"""Validates a PR body against the XMC Create PR template and assumptions format.

Checks the required `##` sections and `TAG=agy` line from
`.agents/skills/xmc_create_pr/SKILL.md`, and that every top-level `## Assumptions`
bullet reads `<assumption> — <why> — <impact if wrong>` or is exactly
`None — requirements were fully specified.`.
"""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

REQUIRED_SECTIONS = (
    "Summary",
    "Motivation & Context",
    "Key Changes",
    "Assumptions",
    "Verification & Testing",
    "Related Issues / Bugs",
)
NONE_BULLET = "None — requirements were fully specified."
SEPARATOR = " — "

_FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
_HEADING_RE = re.compile(r"^ {0,3}(#{1,6})[ \t]+(.*?)[ \t#]*$")
_TAG_RE = re.compile(r"^TAG=agy\s*$")
_BULLET_RE = re.compile(r"^(?P<indent>[ \t]*)[-*+][ \t]+(?P<text>.*)$")
_CODE_SPAN_RE = re.compile(r"(`+)(.+?)\1")


@dataclass
class _Bullet:
    """A top-level list item and its continuation lines."""

    line: int
    parts: list[str] = field(default_factory=list)

    @property
    def text(self) -> str:
        return " ".join(part.strip() for part in self.parts).strip()


@dataclass
class _Body:
    """Parsed PR body: level-2 sections (fences excluded) and the TAG line."""

    sections: dict[str, list[tuple[int, str]]] = field(default_factory=dict)
    has_tag: bool = False


def _fence_toggle(line: str, fence: str | None) -> str | None:
    """Returns the open fence marker after line, or None when outside a fence."""
    match = _FENCE_RE.match(line)
    if not match:
        return fence
    marker = match.group(1)
    if fence is None:
        return marker
    closes = marker[0] == fence[0] and len(marker) >= len(fence)
    return None if closes and not line.strip().strip(marker[0]) else fence


def parse_body(text: str) -> _Body:
    """Splits text into `##` sections; `#`/`##` headings and `TAG=` end a section.

    Args:
        text: PR body markdown (LF or CRLF line endings).

    Returns:
        Parsed body. Fenced lines are dropped, so headings inside fences are ignored.
    """
    body, current, fence = _Body(), None, None
    for number, line in enumerate(text.splitlines(), start=1):
        was_fenced, fence = fence is not None, _fence_toggle(line, fence)
        if was_fenced or fence is not None:
            continue
        heading = _HEADING_RE.match(line)
        if heading and len(heading.group(1)) <= 2:
            current = heading.group(2) if len(heading.group(1)) == 2 else None
            if current is not None:
                body.sections.setdefault(current, [])
            continue
        if _TAG_RE.match(line):
            body.has_tag, current = True, None
            continue
        if current is not None:
            body.sections[current].append((number, line))
    return body


def collect_bullets(lines: list[tuple[int, str]]) -> list[_Bullet]:
    """Groups section lines into top-level bullets, ignoring nested bullets.

    A standalone `None — requirements were fully specified.` paragraph line, which the
    template allows in place of bullets, is returned as a bullet too.

    Args:
        lines: (line number, text) pairs of one section, fences already removed.

    Returns:
        Top-level bullets with wrapped continuation lines joined.
    """
    bullets: list[_Bullet] = []
    current: _Bullet | None = None
    for number, line in lines:
        match = _BULLET_RE.match(line)
        if not line.strip():
            current = None
        elif match and len(match.group("indent").expandtabs(4)) < 2:
            current = _Bullet(line=number, parts=[match.group("text")])
            bullets.append(current)
        elif match:
            current = None
        elif line.strip() == NONE_BULLET:
            bullets.append(_Bullet(line=number, parts=[NONE_BULLET]))
            current = None
        elif current is not None:
            current.parts.append(line)
    return bullets


def assumption_violation(text: str) -> str | None:
    """Returns why an assumption bullet is malformed, or None when valid.

    Args:
        text: Bullet text without the list marker.

    Returns:
        Human-readable reason, or None for a valid bullet.
    """
    if text == NONE_BULLET:
        return None
    parts = _CODE_SPAN_RE.sub("code", text).split(SEPARATOR)
    if len(parts) < 3:
        return (
            f"has {len(parts) - 1} ' — ' separator(s) outside code spans; expected "
            "'<assumption> — <why it was assumed> — <impact if wrong>'"
        )
    if any(not part.strip() for part in parts):
        return "has an empty part between ' — ' separators"
    return None


def _section_violations(body: _Body) -> list[dict[str, Any]]:
    """Reports missing required sections and a missing TAG line."""
    violations = [
        {"line": None, "text": f"## {name}", "reason": "missing required section"}
        for name in REQUIRED_SECTIONS
        if name not in body.sections
    ]
    if not body.has_tag:
        violations.append({"line": None, "text": "TAG=agy", "reason": "missing TAG=agy line"})
    return violations


def _assumption_violations(bullets: list[_Bullet], present: bool) -> list[dict[str, Any]]:
    """Reports malformed or absent `## Assumptions` bullets."""
    if present and not bullets:
        return [{"line": None, "text": "## Assumptions", "reason": "section has no bullets"}]
    return [
        {"line": bullet.line, "text": bullet.text, "reason": reason}
        for bullet in bullets
        if (reason := assumption_violation(bullet.text)) is not None
    ]


def check_body(text: str) -> dict[str, Any]:
    """Validates PR body text.

    Args:
        text: PR body markdown.

    Returns:
        Found section names and the number of non-`None` assumption bullets.

    Raises:
        HandledError: If any required element is missing or an assumption is malformed.
    """
    body = parse_body(text)
    bullets = collect_bullets(body.sections.get("Assumptions", []))
    violations = _section_violations(body) + _assumption_violations(
        bullets, "Assumptions" in body.sections
    )
    if violations:
        raise HandledError(
            f"PR body has {len(violations)} template violation(s).",
            "Add each missing section or TAG=agy line from the template in "
            ".agents/skills/xmc_create_pr/SKILL.md §3 step 6, and rewrite every flagged "
            "## Assumptions bullet as '<assumption> — <why it was assumed> — <impact if "
            f"wrong>' (or exactly '{NONE_BULLET}'), then rerun this check.",
            details={"violations": violations},
        )
    return {
        "sections": list(body.sections),
        "assumption_count": sum(bullet.text != NONE_BULLET for bullet in bullets),
    }


def _read_body(body_file: str) -> str:
    """Reads the PR body from a file path, or stdin when body_file is '-'."""
    if body_file == "-":
        return sys.stdin.read()
    try:
        return Path(body_file).read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as e:
        raise HandledError(
            f"Cannot read PR body file {body_file}: {e}",
            "Write the PR body to a UTF-8 markdown file (e.g. pr_body.md) and pass it "
            "via --body-file.",
        ) from e


def check_pr_body(args: argparse.Namespace) -> dict[str, Any]:
    """Runs the PR body check for the CLI.

    Args:
        args: Parsed CLI namespace containing body_file.

    Returns:
        Result of check_body.
    """
    return check_body(_read_body(args.body_file))


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser.

    Returns:
        Configured ArgumentParser instance.
    """
    parser = argparse.ArgumentParser(
        description=(
            "Validate a PR body's required sections, TAG=agy line, and "
            "'<assumption> — <why> — <impact if wrong>' assumption bullets."
        ),
    )
    parser.add_argument(
        "--body-file", required=True, help="PR body markdown file, or '-' for stdin."
    )
    return parser


def main() -> None:
    """Run the PR body checker CLI."""
    run_tool(build_parser(), check_pr_body)


if __name__ == "__main__":
    main()
