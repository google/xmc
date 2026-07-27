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

"""Tool for extracting structured git diff data for rule and skill modifications."""

import argparse
import subprocess
from typing import Any

from tool_utils import HandledError, run_tool


def get_git_diff(target_dir: str, diff_spec: str | None = None) -> tuple[str, list[str]]:
    """Retrieves git diff content and changed file list for target directory."""
    spec = diff_spec or "HEAD"
    cmd = ["git", "diff", spec, "--", target_dir]

    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True)
        diff_text = res.stdout
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to execute git diff: {e.stderr}",
            "Verify that you are inside a valid git workspace.",
        ) from e
    except OSError as e:
        raise HandledError(
            f"Git execution failed: {e}",
            "Ensure git CLI is installed and accessible in system PATH.",
        ) from e

    # Extract list of changed files
    name_cmd = ["git", "diff", "--name-only", spec, "--", target_dir]
    try:
        name_res = subprocess.run(name_cmd, capture_output=True, text=True, check=True)
        changed_files = [f.strip() for f in name_res.stdout.splitlines() if f.strip()]
    except (subprocess.CalledProcessError, OSError):
        changed_files = []

    return diff_text, changed_files


def extract_diff_structure(changed_files: list[str], diff_text: str) -> dict[str, Any]:
    """Extracts structured diff data (category, added lines, removed lines, raw diff)."""
    is_skill = any(".agents/skills/" in f for f in changed_files)
    is_rule = any(".agents/rules/" in f or "AGENTS.md" in f for f in changed_files)

    if is_skill and is_rule:
        category = "mixed"
    elif is_skill:
        category = "skill"
    elif is_rule:
        category = "rule"
    else:
        category = "other"

    added_lines = [
        line[1:].strip()
        for line in diff_text.splitlines()
        if line.startswith("+") and not line.startswith("+++")
    ]
    removed_lines = [
        line[1:].strip()
        for line in diff_text.splitlines()
        if line.startswith("-") and not line.startswith("---")
    ]

    return {
        "changed_files": changed_files,
        "category": category,
        "added_lines": added_lines,
        "removed_lines": removed_lines,
        "raw_diff": diff_text,
    }


def analyze_diff_and_synthesize(args: argparse.Namespace) -> dict[str, Any]:
    """Main function for extracting structured git diff data."""
    target_dir = args.target_dir
    diff_spec = args.diff_spec

    diff_text, changed_files = get_git_diff(target_dir, diff_spec)
    if not diff_text or not changed_files:
        raise HandledError(
            f"No git diff found under target directory '{target_dir}'.",
            "Ensure you have modified files in your worktree under '.agents/' before running the benchmark.",
        )

    return extract_diff_structure(changed_files, diff_text)


def main() -> None:
    """Main entrypoint for analyze_diff_and_synthesize tool."""
    parser = argparse.ArgumentParser(
        description="Extract structured git diff data for rules/skills modifications."
    )
    parser.add_argument(
        "--target-dir",
        default=".agents",
        help="Target directory to inspect for git diffs (default: .agents).",
    )
    parser.add_argument(
        "--diff-spec",
        default=None,
        help="Optional git diff specification (e.g. HEAD, origin/main, commit_hash).",
    )
    run_tool(parser, analyze_diff_and_synthesize)


if __name__ == "__main__":
    main()
