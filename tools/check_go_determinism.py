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

# Match os.Setenv(...) calls, preventing false matches on struct members (e.g. mock.os.Setenv)
SETENV_PATTERN = re.compile(r"(?<![\w.])os\s*\.\s*Setenv\s*\(")

# Match long time.Sleep calls (e.g. time.Sleep(16 * time.Second), time.Sleep(time.Second * 5))
LONG_SLEEP_PATTERN = re.compile(
    r"(?<![\w.])time\s*\.\s*Sleep\s*\(\s*(?:(?:\d+\s*\*\s*)?time\.(?:Second|Minute|Hour)|time\.(?:Second|Minute|Hour)\s*\*\s*\d+)"
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


def mask_comments_and_strings(content: str) -> tuple[str, set[int], set[int]]:
    """Masks comments, string literals, and rune literals with spaces.

    Newlines are strictly preserved so that line numbers remain exact.

    Returns:
        tuple of:
        - sanitized string with comments/strings blanked out
        - set of line numbers where nolint:determinism was found
        - set of line numbers where nolint:sleep was found
    """
    chars = list(content)
    n = len(chars)
    nolint_determinism_lines: set[int] = set()
    nolint_sleep_lines: set[int] = set()
    line_num = 1
    i = 0
    while i < n:
        c = chars[i]
        if c == "\n":
            line_num += 1
            i += 1
            continue

        # Single-line comment: // ...
        if c == "/" and i + 1 < n and chars[i + 1] == "/":
            start = i
            comment_line = line_num
            while i < n and chars[i] != "\n":
                i += 1
            comment_text = content[start:i]
            if "nolint:determinism" in comment_text:
                nolint_determinism_lines.add(comment_line)
            if "nolint:sleep" in comment_text:
                nolint_sleep_lines.add(comment_line)
            for j in range(start, i):
                chars[j] = " "
            continue

        # Multi-line block comment: /* ... */
        if c == "/" and i + 1 < n and chars[i + 1] == "*":
            start = i
            start_line = line_num
            i += 2
            while i + 1 < n and not (chars[i] == "*" and chars[i + 1] == "/"):
                if chars[i] == "\n":
                    line_num += 1
                i += 1
            if i + 1 < n:
                i += 2
            else:
                i = n
            comment_text = content[start:i]
            if "nolint:determinism" in comment_text:
                for l_idx in range(start_line, line_num + 1):
                    nolint_determinism_lines.add(l_idx)
            if "nolint:sleep" in comment_text:
                for l_idx in range(start_line, line_num + 1):
                    nolint_sleep_lines.add(l_idx)
            for j in range(start, i):
                if chars[j] != "\n":
                    chars[j] = " "
            continue

        # String literal: "..." or `...`
        if c in ('"', "`"):
            quote = c
            i += 1
            while i < n:
                if chars[i] == "\n":
                    line_num += 1
                if quote == '"' and chars[i] == "\\" and i + 1 < n:
                    chars[i] = " "
                    if chars[i + 1] != "\n":
                        chars[i + 1] = " "
                    i += 2
                    continue
                if chars[i] == quote:
                    chars[i] = " "
                    i += 1
                    break
                chars[i] = " " if chars[i] != "\n" else "\n"
                i += 1
            continue

        # Rune literal: '...'
        if c == "'":
            i += 1
            while i < n:
                if chars[i] == "\n":
                    line_num += 1
                    break
                if chars[i] == "\\" and i + 1 < n:
                    chars[i] = " "
                    if chars[i + 1] != "\n":
                        chars[i + 1] = " "
                    i += 2
                    continue
                if chars[i] == "'":
                    chars[i] = " "
                    i += 1
                    break
                chars[i] = " "
                i += 1
            continue

        i += 1

    return "".join(chars), nolint_determinism_lines, nolint_sleep_lines


def check_file(file_path: Path) -> list[str]:
    """Scans a single Go test file for determinism violations."""
    try:
        content = file_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as e:
        return [f"{file_path}: Unable to read file: {e}"]

    sanitized, nolint_det, nolint_sleep = mask_comments_and_strings(content)
    violations: list[str] = []

    for match in SETENV_PATTERN.finditer(sanitized):
        start_line = content[: match.start()].count("\n") + 1
        end_line = content[: match.end()].count("\n") + 1
        if any(line_idx in nolint_det for line_idx in range(start_line - 1, end_line + 1)):
            continue
        violations.append(
            f"{file_path}:{start_line}: "
            f"os.Setenv used in test. Use t.Setenv(...) to guarantee cleanup and prevent environment pollution."
        )

    for match in LONG_SLEEP_PATTERN.finditer(sanitized):
        start_line = content[: match.start()].count("\n") + 1
        end_line = content[: match.end()].count("\n") + 1
        if any(line_idx in nolint_sleep for line_idx in range(start_line - 1, end_line + 1)):
            continue
        violations.append(
            f"{file_path}:{start_line}: "
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
