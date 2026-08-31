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

"""YAML boolean parsing and key flattening utilities."""

import re
from typing import Any


def _flatten_yaml_dict(data: dict[str, Any], prefix: str = "") -> dict[str, bool]:
    """Recursively flattens nested dictionaries into dot-separated boolean keys."""
    flat: dict[str, bool] = {}
    for k, v in data.items():
        key = f"{prefix}.{k}" if prefix else str(k)
        if isinstance(v, dict):
            flat.update(_flatten_yaml_dict(v, key))
        elif isinstance(v, bool):
            if key.startswith("vars."):
                key = key[5:]
            flat[key] = v
    return flat


def _scan_yaml_lines(yaml_content: str) -> dict[str, bool]:
    """Fallback indentation-aware scanner extracting nested boolean keys."""
    flags: dict[str, bool] = {}
    stack: list[tuple[int, str]] = []

    for line in yaml_content.splitlines():
        if not line.strip() or line.strip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        trimmed = line.strip()
        match = re.match(r"^([a-zA-Z0-9_.-]+):\s*(true|false)\b", trimmed, re.IGNORECASE)
        if match:
            key = match.group(1)
            val = match.group(2).lower() == "true"
            while stack and stack[-1][0] >= indent:
                stack.pop()
            full_key = ".".join([p[1] for p in stack] + [key])
            if full_key.startswith("vars."):
                full_key = full_key[5:]
            flags[full_key] = val
        else:
            parent_match = re.match(r"^([a-zA-Z0-9_.-]+):", trimmed)
            if parent_match:
                key = parent_match.group(1)
                while stack and stack[-1][0] >= indent:
                    stack.pop()
                stack.append((indent, key))

    return flags


def parse_yaml_booleans(yaml_content: str) -> dict[str, bool]:
    """Extracts boolean flags from YAML content, supporting nested keys."""
    if not yaml_content or not yaml_content.strip():
        return {}

    try:
        import yaml

        data = yaml.safe_load(yaml_content)
        if isinstance(data, dict):
            return _flatten_yaml_dict(data)
    except Exception:
        pass

    return _scan_yaml_lines(yaml_content)
