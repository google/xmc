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

"""Lint keeping nightly matrix launch args and verifier expectations in sync.

For every `.github/workflows/nightly-examples-e2e.yml` matrix entry, each launch
flag in `CONTRACT` (e.g. `--num_work_units=3` in `args`) must equal its matrix
field (e.g. `expected_work_units: 3`), and a field must not exist without its
flag. The verify step must also forward each field as `--<field>=` so the
verifier asserts what the launcher actually ran.
"""

from __future__ import annotations

import argparse
import re
import shlex
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, parse_positive_int, run_tool  # noqa: E402
from tools.nightly_tests.workflow_matrix import WORKFLOW_REL, parse_matrix_items  # noqa: E402

# Launch flag in matrix `args` -> matrix field forwarded as the verifier flag of the same name.
CONTRACT: dict[str, str] = {
    "--num_work_units": "expected_work_units",
    "--duration": "expected_duration_sec",
}
# Step `id` of the workflow step that runs the verifier and must forward every CONTRACT field.
VERIFY_STEP_ID = "verify-example"
_VERIFY_STEP_ID_RE = re.compile(rf"^\s*(?:-\s+)?id:\s*['\"]?{re.escape(VERIFY_STEP_ID)}['\"]?\s*$")
_UNSET = "<unset>"


def launch_flag_values(args: str) -> dict[str, str]:
    """Returns `CONTRACT` launch flag values from `args` (`--flag=value` or `--flag value`).

    A repeated flag resolves to its last value, matching absl/argparse behavior. In the
    space form, a following token that starts with "--" is another flag, so the value is "".
    """
    tokens = shlex.split(args)
    values: dict[str, str] = {}
    for index, token in enumerate(tokens):
        name, sep, value = token.partition("=")
        if name not in CONTRACT:
            continue
        next_token = tokens[index + 1] if index + 1 < len(tokens) else ""
        values[name] = value if sep else ("" if next_token.startswith("--") else next_token)
    return values


def item_violations(item: dict[str, str]) -> list[str]:
    """Returns contract violations for a single parsed matrix entry."""
    item_id = item.get("id", "<missing id>")
    try:
        launched = launch_flag_values(item.get("args", ""))
    except ValueError as err:
        return [f"matrix entry '{item_id}': cannot parse args ({err})."]
    violations = []
    for flag, field in CONTRACT.items():
        arg_value, field_value = launched.get(flag), item.get(field)
        if arg_value is None and field_value is None:
            continue
        expected = parse_positive_int(field_value)
        if expected is not None and parse_positive_int(arg_value) == expected:
            continue
        violations.append(
            f"matrix entry '{item_id}': args {flag}={arg_value or _UNSET} but "
            f"{field}={field_value or _UNSET}; both must be set to the same positive integer."
        )
    return violations


def _indent(line: str) -> int:
    """Returns the number of leading whitespace characters in `line`."""
    return len(line) - len(line.lstrip())


def _ends_step(line: str, step_indent: int) -> bool:
    """Returns whether `line` starts a sibling step or leaves the steps list.

    Blank and comment lines never end a step, whatever their indentation.
    """
    stripped = line.lstrip()
    if not stripped or stripped.startswith("#"):
        return False
    indent = _indent(line)
    return indent < step_indent or (indent == step_indent and stripped.startswith("- "))


def _step_start(lines: list[str], id_index: int) -> int:
    """Returns the index of the "- " line opening the step that owns `lines[id_index]`.

    The opener is the id line itself (`- id: ...`) or the nearest preceding "- " line
    indented less than `id:`; deeper "- " lines belong to nested values such as run blocks.
    """
    if lines[id_index].lstrip().startswith("- "):
        return id_index
    id_indent = _indent(lines[id_index])
    return next(
        (
            i
            for i in range(id_index - 1, -1, -1)
            if lines[i].lstrip().startswith("- ") and _indent(lines[i]) < id_indent
        ),
        id_index,
    )


def verify_step_block(workflow_text: str) -> str:
    """Returns the text of the step with `id: verify-example`, or "" if absent."""
    lines = workflow_text.splitlines()
    id_index = next((i for i, line in enumerate(lines) if _VERIFY_STEP_ID_RE.match(line)), None)
    if id_index is None:
        return ""
    start = _step_start(lines, id_index)
    step_indent = _indent(lines[start])
    end = next(
        (i for i in range(start + 1, len(lines)) if _ends_step(lines[i], step_indent)),
        len(lines),
    )
    return "\n".join(lines[start:end])


def wiring_violations(workflow_text: str) -> list[str]:
    """Returns violations for contract fields the verify step does not forward."""
    block = verify_step_block(workflow_text)
    if not block:
        return [f"verify step (id: {VERIFY_STEP_ID}) not found in the nightly workflow."]
    return [
        f"verify step must forward matrix.example.{field} as --{field}=."
        for field in CONTRACT.values()
        if f"matrix.example.{field}" not in block or f"--{field}=" not in block
    ]


def audit_workflow(workflow_text: str, items: list[dict[str, str]]) -> list[str]:
    """Returns all contract violations for the workflow text and its parsed matrix `items`."""
    if not items:
        return ["no matrix entries found in the nightly workflow."]
    violations = wiring_violations(workflow_text)
    for item in items:
        violations.extend(item_violations(item))
    return violations


def _read_workflow(workflow_path: Path) -> str:
    """Reads the workflow file, converting I/O and decoding failures to `HandledError`."""
    if not workflow_path.is_file():
        raise HandledError(
            f"Workflow not found: {workflow_path}", "Pass the repository root via --repo-root."
        )
    try:
        return workflow_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as err:
        raise HandledError(
            f"Cannot read workflow {workflow_path}: {err}",
            "Check the file's permissions and that it is valid UTF-8.",
        ) from err


def check_matrix_contract(args: argparse.Namespace) -> dict[str, Any]:
    """Runs the audit against `--repo-root` and raises `HandledError` on violations."""
    workflow_text = _read_workflow(Path(args.repo_root) / WORKFLOW_REL)
    items = parse_matrix_items(workflow_text)
    if violations := audit_workflow(workflow_text, items):
        raise HandledError(
            f"Found {len(violations)} nightly matrix contract violation(s).",
            f"Keep launch args and expected_* fields in {WORKFLOW_REL} equal "
            "(see tools/nightly_tests/README.md).",
            details={"violations": violations},
        )
    return {"entries_checked": len(items), "violations": 0}


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Check nightly matrix launch args match verifier expected_* fields.",
    )
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    return parser


def main() -> None:
    """CLI entrypoint executing `check_matrix_contract` via `tool_utils.run_tool`."""
    run_tool(build_parser(), check_matrix_contract)


if __name__ == "__main__":
    main()
