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

"""Tool for analyzing git diff size against repository PR thresholds."""

import argparse
import re
import subprocess
from typing import Any

try:
    from tools.common.tool_utils import HandledError, run_tool
except ImportError:
    from tool_utils import HandledError, run_tool

# Mandatory sizing thresholds (lines added / modified)
PROD_THRESHOLD = 400
TOTAL_CODE_THRESHOLD = 1000
DOC_THRESHOLD = 1000

GENERATED_PATTERNS = (
    r".*\.pb(\.gw)?\.go$",
    r".*_pb2(_grpc)?\.py$",
    r".*(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|Cargo\.lock|MODULE\.bazel\.lock|go\.sum)$",
    r".*\.min\.(js|css)$",
)

TEST_PATTERNS = (
    r".*(_test|\.spec|\.test)\.(go|py|ts|js)$",
    r"(^|.*/)(test|tests|testing|testdouble)/.*",
    r".*(fixture|fake|mock).*\.(go|py|ts|js)$",
)

DOC_PATTERNS = (
    r".*\.(md|markdown|rst)$",
    r"(^|.*/)docs/.*",
)


def classify_file(file_path: str) -> str:
    """Classifies a relative path as 'generated', 'test', 'doc', or 'prod'."""
    normalized = file_path.replace("\\", "/").strip()
    if any(re.match(p, normalized, re.IGNORECASE) for p in GENERATED_PATTERNS):
        return "generated"
    if any(re.match(p, normalized, re.IGNORECASE) for p in DOC_PATTERNS):
        return "doc"
    if any(re.match(p, normalized, re.IGNORECASE) for p in TEST_PATTERNS):
        return "test"
    return "prod"


def get_diff_entries(diff_spec: str | None = None) -> list[tuple[int, str]]:
    """Extracts added line counts and filepaths from git diff."""
    cmd = ["git", "diff", "--numstat"]
    if diff_spec:
        cmd.append(diff_spec)

    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to execute git diff: {e.stderr.strip()}",
            "Verify that you are inside a valid git workspace and base ref exists.",
        ) from e

    entries: list[tuple[int, str]] = []
    for line in res.stdout.splitlines():
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0].isdigit():
            added, path = int(parts[0]), parts[2]
            entries.append((added, path))

    return entries


def analyze_diff_size(args: argparse.Namespace) -> dict[str, Any]:
    """Evaluates diff size against mandatory PR sizing thresholds."""
    entries = get_diff_entries(args.base_ref)

    prod, test, doc, gen = 0, 0, 0, 0
    for added, path in entries:
        category = classify_file(path)
        if category == "prod":
            prod += added
        elif category == "test":
            test += added
        elif category == "doc":
            doc += added
        elif category == "generated":
            gen += added

    total_code = prod + test
    exceeded_reasons: list[str] = []
    if prod >= PROD_THRESHOLD:
        exceeded_reasons.append(
            f"Production code ({prod} lines) exceeds threshold ({PROD_THRESHOLD} lines)."
        )
    if total_code >= TOTAL_CODE_THRESHOLD:
        exceeded_reasons.append(
            f"Total code (prod + tests = {total_code} lines) exceeds threshold ({TOTAL_CODE_THRESHOLD} lines)."
        )
    if doc >= DOC_THRESHOLD:
        exceeded_reasons.append(
            f"Documentation diff ({doc} lines) exceeds threshold ({DOC_THRESHOLD} lines)."
        )

    should_split = len(exceeded_reasons) > 0
    return {
        "thresholds": {
            "prod_code": PROD_THRESHOLD,
            "total_code": TOTAL_CODE_THRESHOLD,
            "docs": DOC_THRESHOLD,
        },
        "metrics": {
            "prod_code_lines": prod,
            "test_code_lines": test,
            "total_code_lines": total_code,
            "doc_lines": doc,
            "generated_lines": gen,
        },
        "threshold_exceeded": should_split,
        "exceeded_reasons": exceeded_reasons,
        "should_trigger_split_verifier": should_split,
    }


def main() -> None:
    """Main entrypoint for analyze_diff_size tool."""
    parser = argparse.ArgumentParser(
        description="Analyze git diff size against production, total code, and doc thresholds."
    )
    parser.add_argument(
        "--base-ref",
        default="origin/main...",
        help="Git diff base reference (default: 'origin/main...').",
    )
    run_tool(parser, analyze_diff_size)


if __name__ == "__main__":
    main()
