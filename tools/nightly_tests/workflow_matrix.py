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

"""Pure parser and filter validator for the Nightly Examples E2E workflow matrix."""

import pathlib
import re
from collections.abc import Iterator

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW_REL = pathlib.Path(".github/workflows/nightly-examples-e2e.yml")
DEFAULT_WORKFLOW = REPO_ROOT / WORKFLOW_REL

_MATRIX_KEYS = (
    "id",
    "name",
    "example_path",
    "entrypoint",
    "args",
    "verify_module",
    "expected_work_units",
    "expected_duration_sec",
)
_KEY_RE = re.compile(rf"^\s*(?:-\s+)?(?P<key>{'|'.join(_MATRIX_KEYS)}):(?P<value>.*)$")
_ITEM_START_RE = re.compile(r"^\s*-\s")


def _unquote(raw: str) -> str:
    """Return a scalar YAML value without surrounding quotes or a trailing comment."""
    value = raw.strip()
    if value[:1] in ("'", '"'):
        end = value.find(value[0], 1)
        return value[1:end] if end > 0 else value[1:]
    return value.split(" #", 1)[0].strip()


def _matrix_lines(workflow_text: str) -> Iterator[str]:
    """Yield only lines nested under a `matrix:` key, using indentation to scope blocks."""
    matrix_indent: int | None = None
    for line in workflow_text.splitlines():
        stripped = line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(stripped)
        if matrix_indent is not None and indent <= matrix_indent:
            matrix_indent = None
        if matrix_indent is not None:
            yield line
        elif stripped.startswith("matrix:"):
            matrix_indent = indent


def parse_matrix_items(workflow_text: str) -> list[dict[str, str]]:
    """Return tracked scalar keys for each YAML list item scoped under `matrix:` blocks."""
    items: list[dict[str, str]] = []
    # Indentation of the '-' opening the active list item, or None outside an item.
    item_indent: int | None = None
    for line in _matrix_lines(workflow_text):
        indent = len(line) - len(line.lstrip())
        if _ITEM_START_RE.match(line):
            items.append({})
            item_indent = indent
        elif item_indent is not None and indent <= item_indent:
            item_indent = None
        key_match = _KEY_RE.match(line)
        if key_match and item_indent is not None:
            items[-1][key_match.group("key")] = _unquote(key_match.group("value"))
    return [item for item in items if item]


def resolve_workflow_path(repo_dir: pathlib.Path | None = None) -> pathlib.Path:
    """Resolve `.github/workflows/nightly-examples-e2e.yml` from `repo_dir` or default root."""
    if repo_dir and repo_dir != pathlib.Path("."):
        return repo_dir / WORKFLOW_REL
    return WORKFLOW_REL if WORKFLOW_REL.is_file() else DEFAULT_WORKFLOW


def validate_examples_filter(raw_filter: str, matrix_items: list[dict[str, str]]) -> frozenset[str]:
    """Validate comma-separated `examples_filter` IDs against `matrix.example.id` entries.

    Returns:
        The lower-cased filter IDs, or an empty set when `raw_filter` is empty.
    """
    if not raw_filter:
        return frozenset()
    valid_ids = {item["id"] for item in matrix_items if item.get("id")}
    if not valid_ids:
        raise ValueError("no matrix.example.id entries found in workflow")
    lower_ids = {eid.lower() for eid in valid_ids}
    tokens = [token.lower() for token in raw_filter.split(",")]
    bad = [token for token in tokens if token not in lower_ids]
    if bad:
        raise ValueError(
            f"unknown or malformed examples_filter entry {bad!r}; valid ids: {', '.join(sorted(valid_ids))}"
        )
    return frozenset(tokens)
