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

"""Lint GitHub Actions workflows for null-unsafe boolean comparisons on `inputs`.

`inputs` is null on events that carry no inputs (e.g. `pull_request`), and the
expression language's loose `==` coerces null and booleans to numbers, so
`inputs.x == false` is true and `inputs.x != false` is false on those events.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

# Events whose payload has no `inputs`, so `inputs.<name>` evaluates to null.
INPUTLESS_EVENTS = frozenset(
    {"pull_request", "pull_request_target", "merge_group", "push", "schedule"}
)

_WORKFLOWS_DIR = ".github/workflows"
# Text-based parsing keeps this stdlib-only and immune to YAML 1.1 reading `on` as True.
_ON_KEY_RE = re.compile(r"""^(?:on|"on"|'on')[ ]*:(?P<rest>.*)$""")
_COMMENT_RE = re.compile(r"(?:^|[ \t]+)#.*$")
_TAB_INDENT_RE = re.compile(r"^ *\t", re.M)
_EXPR_RE = re.compile(r"\$\{\{(.*?)\}\}", re.S)
_IF_RE = re.compile(r"^[ ]*(?:-[ ]+)?(?P<key>if)[ ]*:[ ]*(?P<value>.*)$")
# An empty value or a block scalar header (`|`, `>-`, ...) continues on deeper lines.
_BLOCK_VALUE_RE = re.compile(r"^(?:[|>][-+0-9]*)?[ ]*(?:#.*)?$")
_INPUT_REF = r"(?:github\.event\.)?inputs\.(?P<{group}>[A-Za-z_][\w-]*)"
_COMPARISON_RE = re.compile(
    r"(?<![\w.])"
    + _INPUT_REF.format(group="lhs")
    + r"\s*(?:==|!=)\s*(?P<lhs_lit>true|false)(?![\w-])"
    r"|(?<![\w.'\"-])(?P<rhs_lit>true|false)\s*(?:==|!=)\s*"
    + _INPUT_REF.format(group="rhs")
    + r"(?![\w-])"
)


def _malformed(path: str, reason: str) -> HandledError:
    """Builds the handled error for a workflow whose triggers cannot be parsed."""
    return HandledError(
        f"Cannot parse workflow triggers in {path}: {reason}",
        "Fix the workflow YAML (space indentation, a top-level `on:` key, closed "
        "flow collections) and rerun; actionlint reports the exact syntax error.",
    )


def _unquote(token: str) -> str:
    """Strips surrounding whitespace and matching YAML quotes from a scalar."""
    token = token.strip()
    if len(token) >= 2 and token[0] == token[-1] and token[0] in "'\"":
        return token[1:-1]
    return token


def _split_flow(body: str) -> list[str]:
    """Splits a YAML flow collection body on top-level commas."""
    items: list[str] = []
    current: list[str] = []
    depth = 0
    for char in body:
        depth += {"[": 1, "{": 1, "]": -1, "}": -1}.get(char, 0)
        if char == "," and depth == 0:
            items.append("".join(current))
            current = []
            continue
        current.append(char)
    items.append("".join(current))
    return [item for item in items if item.strip()]


def _flow_triggers(value: str, path: str) -> set[str]:
    """Parses `[a, b]` or `{a: ..., b: ...}` trigger collections."""
    closer = "]" if value[0] == "[" else "}"
    if not value.endswith(closer):
        raise _malformed(path, f"unterminated flow collection `{value}`")
    return {_unquote(item.split(":", 1)[0]) for item in _split_flow(value[1:-1])}


def _block_triggers(lines: Sequence[str], path: str) -> set[str]:
    """Parses block-style trigger lists or mappings that follow a bare `on:` line."""
    triggers: set[str] = set()
    child_indent: int | None = None
    for raw in lines:
        line = _COMMENT_RE.sub("", raw).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip(" "))
        if indent == 0 and not line.startswith("- "):
            break
        child_indent = indent if child_indent is None else child_indent
        if indent == child_indent:
            entry = line.strip().removeprefix("- ")
            triggers.add(_unquote(entry.split(":", 1)[0]))
    if not triggers:
        raise _malformed(path, "`on:` has no triggers")
    return triggers


def parse_triggers(text: str, path: str) -> set[str]:
    """Returns the workflow's trigger event names.

    Args:
        text: Workflow file contents.
        path: Path used in error messages.

    Returns:
        Set of trigger event names from the top-level `on:` key.

    Raises:
        HandledError: If the file uses tab indentation or has no parseable `on:` key.
    """
    if _TAB_INDENT_RE.search(text):
        raise _malformed(path, "tab characters used for indentation")
    lines = text.splitlines()
    for index, line in enumerate(lines):
        match = _ON_KEY_RE.match(line)
        if not match:
            continue
        value = _COMMENT_RE.sub("", match.group("rest")).strip()
        if not value:
            return _block_triggers(lines[index + 1 :], path)
        if value[0] in "[{":
            return _flow_triggers(value, path)
        return {_unquote(value)}
    raise _malformed(path, "no top-level `on:` key")


def _is_comment_line(text: str, offset: int) -> bool:
    """Reports whether the line containing offset is a full-line YAML comment."""
    line_start = text.rfind("\n", 0, offset) + 1
    return text[line_start:offset].lstrip().startswith("#")


def _if_value_segments(text: str) -> Iterator[tuple[int, str]]:
    """Yields (offset, text) for `if:` values written without `${{ }}`."""
    offset, lines = 0, text.splitlines(keepends=True)
    for index, line in enumerate(lines):
        start = offset
        offset += len(line)
        match = _IF_RE.match(line.rstrip("\r\n"))
        if not match or "${{" in match.group("value"):
            continue
        yield start + match.start("value"), match.group("value")
        if _BLOCK_VALUE_RE.match(match.group("value")):
            yield from _continuation_segments(lines[index + 1 :], offset, match.start("key"))


def _continuation_segments(
    lines: Sequence[str], offset: int, key_column: int
) -> Iterator[tuple[int, str]]:
    """Yields (offset, line) for lines indented deeper than key_column."""
    for line in lines:
        if line.strip() and len(line) - len(line.lstrip(" ")) <= key_column:
            return
        yield offset, line
        offset += len(line)


def _expression_segments(text: str) -> Iterator[tuple[int, str]]:
    """Yields (offset, text) for every expression context in a workflow."""
    for match in _EXPR_RE.finditer(text):
        if not _is_comment_line(text, match.start()):
            yield match.start(1), match.group(1)
    yield from _if_value_segments(text)


def _remediation(name: str, literal: str) -> str:
    """Builds the fix hint for one unsafe comparison against literal."""
    return (
        f"`inputs.{name}` is null on input-less events and loose `==` coerces null and "
        f"booleans to numbers. Use `format('{{0}}', inputs.{name}) == '{literal}'` for an "
        f"explicit check, or plain truthiness / `inputs.{name} || <default>` when null "
        "should mean the default."
    )


def find_violations(text: str, path: str) -> list[dict[str, Any]]:
    """Returns unsafe boolean `inputs` comparisons in one workflow file.

    Args:
        text: Workflow file contents.
        path: Path recorded on each violation.

    Returns:
        Violations sorted by line, empty when the workflow has no input-less trigger.

    Raises:
        HandledError: If the workflow triggers cannot be parsed.
    """
    if not parse_triggers(text, path) & INPUTLESS_EVENTS:
        return []
    found: dict[tuple[int, str], dict[str, Any]] = {}
    for offset, segment in _expression_segments(text):
        for match in _COMPARISON_RE.finditer(segment):
            position = offset + match.start()
            line = text.count("\n", 0, position) + 1
            name = match.group("lhs") or match.group("rhs")
            literal = match.group("lhs_lit") or match.group("rhs_lit")
            found[(position, match.group(0))] = {
                "path": path,
                "line": line,
                "match": match.group(0),
                "remediation": _remediation(name, literal),
            }
    return [found[key] for key in sorted(found)]


def _read_workflow(path: Path) -> str:
    """Reads a workflow file as UTF-8, converting failures to handled errors."""
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as e:
        raise HandledError(
            f"Cannot read workflow {path}: {e}",
            "Pass existing UTF-8 workflow files or a valid --repo-root.",
        ) from e


def _target_files(args: argparse.Namespace) -> list[Path]:
    """Resolves explicit paths, or every top-level workflow under --repo-root."""
    if args.paths:
        return [Path(p) for p in args.paths]
    workflows = Path(args.repo_root) / _WORKFLOWS_DIR
    return sorted([*workflows.glob("*.yml"), *workflows.glob("*.yaml")])


def _display_path(path: Path, repo_root: Path) -> str:
    """Returns path relative to repo_root when possible."""
    resolved = path.resolve()
    return str(resolved.relative_to(repo_root)) if resolved.is_relative_to(repo_root) else str(path)


def check_workflow_inputs(args: argparse.Namespace) -> dict[str, Any]:
    """Lints workflows for null-unsafe boolean comparisons on `inputs`.

    Args:
        args: Parsed CLI namespace containing repo_root and optional paths.

    Returns:
        Summary dictionary with files_scanned and violations count.

    Raises:
        HandledError: If a workflow cannot be read or parsed, or violations are found.
    """
    repo_root = Path(args.repo_root).resolve()
    files = _target_files(args)
    violations = [
        violation
        for path in files
        for violation in find_violations(_read_workflow(path), _display_path(path, repo_root))
    ]
    if violations:
        raise HandledError(
            f"Found {len(violations)} null-unsafe boolean comparison(s) on workflow inputs.",
            "Replace each reported comparison as its remediation describes: "
            "`format('{0}', inputs.<name>) == 'false'` (or `== 'true'`) for explicit checks, "
            "or plain truthiness / `inputs.<name> || <default>` when null should mean the "
            "default.",
            details={"violations": violations},
        )
    return {"files_scanned": len(files), "violations": 0}


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser.

    Returns:
        Configured ArgumentParser instance.
    """
    parser = argparse.ArgumentParser(
        description=(
            "Lint .github/workflows for `inputs.<name> ==/!= true|false` comparisons in "
            "workflows that also run on input-less events (pull_request, push, ...)."
        ),
    )
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root directory.")
    parser.add_argument(
        "paths",
        nargs="*",
        help="Workflow files to lint (default: every .github/workflows/*.yml|*.yaml).",
    )
    return parser


def main() -> None:
    """Run the workflow inputs linter CLI."""
    run_tool(build_parser(), check_workflow_inputs)


if __name__ == "__main__":
    main()
