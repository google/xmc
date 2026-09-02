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

"""Linter script to enforce Go test determinism and prevent state pollution.

Checks Go test files (*_test.go) for:
1. os.Setenv usage (must use t.Setenv instead to prevent test pollution).
2. Arbitrary multi-second time.Sleep calls (must use short TTLs, synctest, or polling).
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path

# Match os.Setenv(...) calls
SETENV_PATTERN = re.compile(r"\bos\.Setenv\s*\(")

# Match long time.Sleep calls (e.g. time.Sleep(16 * time.Second), time.Sleep(time.Second * 5))
LONG_SLEEP_PATTERN = re.compile(
    r"\btime\.Sleep\s*\(\s*(?:(?:\d+\s*\*\s*)?time\.(?:Second|Minute|Hour)|(?:\d+)\s*\*\s*time\.Second)"
)

EXCLUDED_DIRS = {
    "vendor",
    "third_party",
    ".git",
    "node_modules",
    "bazel-bin",
    "bazel-out",
    "bazel-testlogs",
    "bazel-xmc",
}


def is_excluded(path: Path) -> bool:
    """Returns True if any parent directory of path is in EXCLUDED_DIRS."""
    return any(part in EXCLUDED_DIRS for part in path.parts)


def check_file(file_path: Path) -> list[str]:
    """Scans a single Go test file for determinism violations."""
    violations: list[str] = []
    try:
        content = file_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as e:
        return [f"{file_path}: Unable to read file: {e}"]

    lines = content.splitlines()
    in_block_comment = False
    for idx, line in enumerate(lines):
        line_num = idx + 1
        stripped = line.strip()
        if in_block_comment:
            if "*/" in stripped:
                in_block_comment = False
            continue
        if stripped.startswith("/*"):
            if "*/" not in stripped:
                in_block_comment = True
            continue
        if stripped.startswith("//"):
            continue

        prev_line = lines[idx - 1] if idx > 0 else ""
        if "nolint:determinism" in line or "nolint:determinism" in prev_line:
            continue

        if SETENV_PATTERN.search(line):
            violations.append(
                f"{file_path}:{line_num}: "
                f"os.Setenv used in test. Use t.Setenv(...) to guarantee cleanup and prevent environment pollution."
            )

        if (
            LONG_SLEEP_PATTERN.search(line)
            and "nolint:sleep" not in line
            and "nolint:sleep" not in prev_line
        ):
            violations.append(
                f"{file_path}:{line_num}: "
                f"arbitrary second-level time.Sleep in test. Inject millisecond TTLs, use synctest, or use wait.Poll."
            )

    return violations


def find_test_files(root: Path) -> list[Path]:
    """Finds all Go test files under root excluding ignored directories."""
    test_files: list[Path] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in EXCLUDED_DIRS]
        for f in filenames:
            if f.endswith("_test.go"):
                p = Path(dirpath) / f
                if not is_excluded(p):
                    test_files.append(p)
    return test_files


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Check Go test files for determinism anti-patterns."
    )
    parser.add_argument(
        "files",
        nargs="*",
        type=Path,
        help="Specific Go test files to check. If omitted, all *_test.go files are checked.",
    )
    args = parser.parse_args()

    files_to_check: list[Path] = []
    if args.files:
        files_to_check.extend(
            f
            for f in args.files
            if f.name.endswith("_test.go") and not is_excluded(f) and f.exists()
        )
    else:
        files_to_check = find_test_files(Path.cwd())

    all_violations: list[str] = []
    for test_file in files_to_check:
        all_violations.extend(check_file(test_file))

    if all_violations:
        print("❌ Go test determinism violations found:")
        for v in all_violations:
            print(f"  {v}")
        return 1

    print(f"✅ Go test determinism check passed ({len(files_to_check)} files checked).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
