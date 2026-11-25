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

"""Renders Docker Buildx Bake variable definitions from versions.json."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

_DEFAULT_VERSIONS_PATH = Path(__file__).resolve().parent / "versions.json"

_TOOL_FIELD_MAP: tuple[tuple[str, str, str], ...] = (
    ("PYTHON_VERSION", "python", "version"),
    ("PYTHON_RELEASE_TAG", "python", "release_tag"),
    ("PYTHON_SHA256", "python", "sha256"),
    ("NODE_VERSION", "node", "version"),
    ("NODE_SHA256", "node", "sha256"),
)


def build_bake_variables(manifest: Mapping[str, Any]) -> dict[str, dict[str, dict[str, str]]]:
    """Converts a parsed versions.json manifest into a Docker Bake variable payload.

    Args:
        manifest: Parsed JSON mapping from ``deploy/ci/images/versions.json``.

    Returns:
        A ``{"variable": {...}}`` dictionary consumable by ``docker buildx bake -f``.

    Raises:
        ValueError: If required keys or non-empty string values are missing.
    """
    base_image = manifest.get("base_image")
    if not isinstance(base_image, str) or not base_image.strip():
        raise ValueError("Manifest is missing a non-empty string 'base_image'.")

    tools = manifest.get("tools")
    if not isinstance(tools, Mapping):
        raise ValueError("Manifest is missing a 'tools' object.")

    variables: dict[str, dict[str, str]] = {"BASE_IMAGE": {"default": base_image}}
    for var_name, tool_key, field_key in _TOOL_FIELD_MAP:
        tool_entry = tools.get(tool_key)
        if not isinstance(tool_entry, Mapping):
            raise ValueError(f"Manifest is missing tool '{tool_key}'.")
        value = tool_entry.get(field_key)
        if not isinstance(value, str) or not value.strip():
            raise ValueError(f"Tool '{tool_key}' is missing a non-empty string '{field_key}'.")
        variables[var_name] = {"default": value}
    return {"variable": variables}


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """Parses CLI arguments for Bake variable rendering.

    Args:
        argv: Optional sequence of CLI arguments.

    Returns:
        Parsed ``argparse.Namespace`` containing ``versions`` and ``output``.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--versions",
        type=Path,
        default=_DEFAULT_VERSIONS_PATH,
        help="Path to versions.json manifest.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Optional output path; writes to stdout when omitted.",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entrypoint that renders Bake variable JSON.

    Args:
        argv: Optional CLI arguments.

    Returns:
        Process exit code (0 on success, 1 on validation error).
    """
    args = parse_args(argv)
    try:
        manifest = json.loads(args.versions.read_text(encoding="utf-8"))
        if not isinstance(manifest, Mapping):
            raise ValueError("Manifest root must be a JSON object.")
        payload = json.dumps(build_bake_variables(manifest), indent=2) + "\n"
        if args.output is not None:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(payload, encoding="utf-8")
        else:
            sys.stdout.write(payload)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
