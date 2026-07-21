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

# YAML Linter and Auto-Formatter for XMC repository.

import os
import re
import sys

try:
    import yaml

    HAS_YAML = True
except ImportError:
    HAS_YAML = False

IGNORE_DIRS = [
    "deploy/helm/templates",
    "deploy/helm/templates_disabled",
    "cmd/ui/node_modules",
    "node_modules",
    ".venv",
    ".git",
    "_worker_notes",
]


def should_ignore(filepath: str) -> bool:
    norm_path = filepath.replace("\\", "/").lstrip("./")
    for d in IGNORE_DIRS:
        if norm_path == d or norm_path.startswith(d + "/"):
            return True
    return False


def check_yaml_syntax(filepath: str):
    if not HAS_YAML:
        return True, None
    try:
        with open(filepath, "r", encoding="utf-8") as f:
            yaml.safe_load(f)
        return True, None
    except Exception as e:
        return False, str(e)


def format_yaml_file(filepath: str) -> bool:
    with open(filepath, "r", encoding="utf-8") as f:
        lines = f.readlines()

    if not lines:
        return False

    new_lines = []
    consecutive_blanks = 0

    for line in lines:
        line_str = line.rstrip("\r\n")
        line_str = line_str.rstrip(" \t")

        if not line_str.strip():
            consecutive_blanks += 1
            if consecutive_blanks > 2:
                continue
            new_lines.append("\n")
        else:
            consecutive_blanks = 0
            new_lines.append(line_str + "\n")

    content = "".join(new_lines).rstrip("\n") + "\n"
    original_content = "".join(lines)

    if content != original_content:
        with open(filepath, "w", encoding="utf-8") as f:
            f.write(content)
        return True
    return False


def lint_file(filepath: str):
    issues = []

    valid, err = check_yaml_syntax(filepath)
    if not valid:
        issues.append(f"YAML Syntax Error: {err}")

    with open(filepath, "r", encoding="utf-8") as f:
        lines = f.readlines()

    for idx, line in enumerate(lines, 1):
        line_str = line.rstrip("\r\n")
        is_comment = line_str.strip().startswith("#")
        is_url = bool(re.search(r"https?://", line_str))

        if len(line_str) > 120 and not is_url and not is_comment:
            issues.append(f"Line {idx}: line too long ({len(line_str)} > 120 characters)")

        if line_str != line_str.rstrip(" \t"):
            issues.append(f"Line {idx}: trailing whitespace")

        if line.startswith("\t") or " \t" in line[: len(line) - len(line.lstrip())]:
            issues.append(f"Line {idx}: indentation uses tabs instead of spaces")

    return issues


def find_yaml_files(start_dir: str = "."):
    yaml_files = []
    for root, dirs, files in os.walk(start_dir):
        dirs[:] = [d for d in dirs if not should_ignore(os.path.join(root, d))]
        for file in files:
            if file.endswith((".yaml", ".yml")):
                full_path = os.path.join(root, file)
                if not should_ignore(full_path):
                    yaml_files.append(full_path)
    return sorted(yaml_files)


def main():
    yaml_files = find_yaml_files()
    print(f"Found {len(yaml_files)} YAML files to check.")

    fixed_count = 0
    for yf in yaml_files:
        if format_yaml_file(yf):
            fixed_count += 1
            print(f"Formatted: {yf}")

    if fixed_count > 0:
        print(f"Auto-formatted {fixed_count} file(s).")

    total_issues = 0
    for yf in yaml_files:
        issues = lint_file(yf)
        if issues:
            print(f"\n❌ {yf}:")
            for issue in issues:
                print(f"   - {issue}")
            total_issues += len(issues)

    if total_issues > 0:
        print(f"\nTotal lint issues found: {total_issues}")
        sys.exit(1)

    print("\n✅ All YAML files passed linting!")
    sys.exit(0)


if __name__ == "__main__":
    main()
