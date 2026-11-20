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

"""Workspace status script for Bazel to emit the repository XMC version."""

from __future__ import annotations

import sys
from pathlib import Path


def _default_version_file() -> Path:
    logical = Path(__file__).parent.parent.parent / "VERSION"
    if logical.is_file():
        return logical
    return Path(__file__).resolve().parents[2] / "VERSION"


DEFAULT_VERSION_FILE = _default_version_file()
FALLBACK_VERSION = "developer-build"


def read_version(version_file: Path = DEFAULT_VERSION_FILE) -> str:
    """Reads and returns the trimmed XMC version from the repository VERSION file.

    Args:
        version_file: Path to the VERSION file.

    Returns:
        The trimmed version string.

    Raises:
        OSError: If the file cannot be read.
        ValueError: If the file is empty or contains whitespace.
    """
    raw = version_file.read_text(encoding="utf-8").strip()
    if not raw or any(ch.isspace() for ch in raw):
        raise ValueError(f"Invalid version in '{version_file}': {raw!r}")
    return raw


def main(version_file: Path = DEFAULT_VERSION_FILE) -> None:
    """Emits STABLE_APP_VERSION for Bazel workspace status stamping."""
    try:
        version = read_version(version_file)
    except Exception as e:
        print(f"Error reading version file '{version_file}': {e}", file=sys.stderr)
        version = FALLBACK_VERSION

    print(f"STABLE_APP_VERSION {version}")


if __name__ == "__main__":
    main()
