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

"""Tool for executing teardown and cleanup of benchmark test resources."""

import argparse
import json
import subprocess
from pathlib import Path
from typing import Any

from tool_utils import HandledError, run_tool


def teardown_resources(
    prs: list[str], branches: list[str], temp_files: list[str]
) -> dict[str, Any]:
    """Executes cleanup for specified PRs, git branches, and temporary files."""
    cleaned_prs = []
    failed_prs = []
    cleaned_branches = []
    failed_branches = []
    cleaned_files = []

    # 1. Cleanup PRs
    for pr_url in prs:
        try:
            cmd = ["gh", "pr", "close", pr_url, "--delete-branch"]
            res = subprocess.run(cmd, capture_output=True, text=True, check=False)
            if res.returncode == 0:
                cleaned_prs.append(pr_url)
            else:
                failed_prs.append({"url": pr_url, "error": res.stderr.strip()})
        except Exception as e:
            failed_prs.append({"url": pr_url, "error": str(e)})

    # 2. Cleanup Git Remote Branches
    for branch in branches:
        clean_branch = branch.replace("origin/", "").replace("origins/", "").strip()
        try:
            cmd = ["git", "push", "origin", "--delete", clean_branch]
            res = subprocess.run(cmd, capture_output=True, text=True, check=False)
            if (
                res.returncode == 0
                or "remote ref does not exist" in res.stderr
                or "no such ref" in res.stderr
            ):
                cleaned_branches.append(clean_branch)

            else:
                failed_branches.append({"branch": branch, "error": res.stderr.strip()})
        except Exception as e:
            failed_branches.append({"branch": branch, "error": str(e)})

    # 3. Cleanup Scratch Files
    for file_path in temp_files:
        try:
            p = Path(file_path)
            if p.exists():
                p.unlink()
                cleaned_files.append(file_path)
        except OSError:
            pass

    return {
        "cleaned_prs": cleaned_prs,
        "failed_prs": failed_prs,
        "cleaned_branches": cleaned_branches,
        "failed_branches": failed_branches,
        "cleaned_files": cleaned_files,
        "success": len(failed_prs) == 0 and len(failed_branches) == 0,
    }


def teardown_benchmark(args: argparse.Namespace) -> dict[str, Any]:
    """Main orchestrator for teardown_benchmark tool."""
    prs = list(args.pr_url or [])
    branches = list(args.git_branch or [])
    temp_files = list(args.temp_file or [])

    if args.manifest:
        manifest_path = Path(args.manifest)
        if not manifest_path.is_file():
            raise HandledError(
                f"Manifest file not found: {args.manifest}",
                "Verify the manifest file path is correct and the file exists.",
            )
        try:
            with open(manifest_path, encoding="utf-8") as f:
                data = json.load(f)
                if not isinstance(data, dict):
                    raise HandledError(
                        f"Manifest file '{args.manifest}' must contain a JSON object.",
                        "Ensure the manifest file is structured as a JSON object/dictionary.",
                    )
                for key, target in [
                    ("pr_urls", prs),
                    ("git_branches", branches),
                    ("temp_files", temp_files),
                ]:
                    val = data.get(key)
                    if val is not None:
                        if isinstance(val, list):
                            target.extend(item for item in val if isinstance(item, str))
                        else:
                            raise HandledError(
                                f"Manifest key '{key}' must be a list, got {type(val).__name__}.",
                                "Ensure the manifest file contains valid lists for resource keys.",
                            )
        except (json.JSONDecodeError, OSError) as e:
            raise HandledError(
                f"Failed to parse manifest file '{args.manifest}': {e}",
                "Ensure manifest file contains valid JSON.",
            ) from e

    if not prs and not branches and not temp_files:
        return {
            "message": "No registered test resources found to clean up.",
            "success": True,
        }

    return teardown_resources(prs, branches, temp_files)


def main() -> None:
    """Main entrypoint for teardown_benchmark tool."""
    parser = argparse.ArgumentParser(
        description="Teardown and clean up real-world benchmark test resources."
    )
    parser.add_argument(
        "--manifest",
        help="Path to JSON benchmark resource manifest file.",
    )
    parser.add_argument(
        "--pr-url",
        action="append",
        help="Specific PR URL to close and cleanup (can be specified multiple times).",
    )
    parser.add_argument(
        "--git-branch",
        action="append",
        help="Specific remote git branch to delete (can be specified multiple times).",
    )
    parser.add_argument(
        "--temp-file",
        action="append",
        help="Specific temporary file path to remove (can be specified multiple times).",
    )
    run_tool(parser, teardown_benchmark)


if __name__ == "__main__":
    main()
