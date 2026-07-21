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

"""Helper script to discover and execute affected Bazel tests for modified files."""

import argparse
import os
import subprocess

from tool_utils import HandledError, run_tool


def get_modified_files(provided_files=None):
    """Returns list of modified file paths relative to git root."""
    if provided_files:
        return provided_files

    try:
        output = subprocess.check_output(["git", "status", "--porcelain"], text=True)
        files = []
        for line in output.splitlines():
            line = line.strip()
            if line:
                parts = line.split(maxsplit=1)
                if len(parts) == 2:
                    files.append(parts[1])
        return files
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to get git status: {e}",
            "Ensure you are running inside a git repository.",
        )


def resolve_bazel_packages(files):
    """Maps modified files to Bazel package query strings."""
    packages = set()
    for f in files:
        if f.startswith(".githooks/") or f.startswith(".agents/"):
            continue
        dirname = os.path.dirname(f)
        while dirname and dirname != ".":
            if os.path.exists(os.path.join(dirname, "BUILD.bazel")) or os.path.exists(
                os.path.join(dirname, "BUILD")
            ):
                packages.add(f"//{dirname}")
                break
            dirname = os.path.dirname(dirname)
    return sorted(list(packages))


def run_affected_tests(args):
    """Finds affected Bazel test targets and executes bazel test."""
    files = get_modified_files(args.files)
    if not files:
        return {"status": "success", "message": "No modified files detected.", "executed_tests": []}

    packages = resolve_bazel_packages(files)
    if not packages:
        return {
            "status": "success",
            "message": "No Bazel package targets affected by modified files.",
            "executed_tests": [],
        }

    targets_to_test = []
    for pkg in packages:
        try:
            query = f"kind('.*_test', {pkg}:all)"
            out = subprocess.check_output(
                ["bazel", "query", query], text=True, stderr=subprocess.DEVNULL
            ).strip()
            if out:
                targets_to_test.extend(out.splitlines())
        except subprocess.CalledProcessError:
            # Fall back to testing all targets in package
            targets_to_test.append(f"{pkg}:all")

    if not targets_to_test:
        return {
            "status": "success",
            "message": "No test targets found in affected packages.",
            "packages": packages,
            "executed_tests": [],
        }

    cmd = ["bazel", "test"] + targets_to_test
    try:
        res = subprocess.run(cmd, capture_output=True, text=True)
        if res.returncode != 0:
            raise HandledError(
                f"Bazel test execution failed (code {res.returncode}).\n{res.stdout}\n{res.stderr}",
                "Fix failing test assertions or build dependencies.",
            )
        return {
            "status": "success",
            "executed_targets": targets_to_test,
            "output": res.stdout.strip(),
        }
    except FileNotFoundError:
        raise HandledError(
            "Bazel executable not found in PATH.",
            "Ensure Bazel is installed and available in environment PATH.",
        )


def main():
    """Main entrypoint for run_affected_tests tool."""
    parser = argparse.ArgumentParser(
        description="Discover and run affected Bazel tests for modified files."
    )
    parser.add_argument(
        "files",
        nargs="*",
        help="Optional list of modified file paths. If omitted, uses 'git status'.",
    )
    run_tool(parser, run_affected_tests)


if __name__ == "__main__":
    main()
