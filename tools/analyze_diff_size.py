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
import sys
from pathlib import Path
from typing import Any, BinaryIO

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.changed_files import list_untracked_files  # noqa: E402
from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

# Mandatory sizing thresholds (lines added / modified)
PROD_THRESHOLD = 400
TOTAL_CODE_THRESHOLD = 1000
DOC_THRESHOLD = 1000

# Prefix git inspects for NUL bytes when deciding whether a file is binary.
_BINARY_SNIFF_BYTES = 8000

# Read size used when streaming a file to count its lines.
_LINE_COUNT_CHUNK_BYTES = 64 * 1024

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


def count_added_lines(file_path: Path) -> int:
    """Returns the lines git numstat would report as added for a new file.

    Mirrors git's binary heuristic (a NUL byte in the first 8000 bytes): binary or
    unreadable files count 0, like numstat's '-'. A final line without a newline counts.
    The file is streamed in fixed-size chunks, so memory use stays bounded.
    """
    try:
        with file_path.open("rb") as stream:
            return _count_stream_lines(stream)
    except OSError:
        return 0


def _count_stream_lines(stream: BinaryIO) -> int:
    """Returns the newline-terminated (plus trailing partial) lines in a binary stream.

    Returns 0 when a NUL byte appears within the first _BINARY_SNIFF_BYTES bytes.
    """
    head = stream.read(_BINARY_SNIFF_BYTES)
    if b"\0" in head:
        return 0
    lines = head.count(b"\n")
    last_byte = head[-1:]
    while chunk := stream.read(_LINE_COUNT_CHUNK_BYTES):
        lines += chunk.count(b"\n")
        last_byte = chunk[-1:]
    if last_byte and last_byte != b"\n":
        lines += 1
    return lines


def get_untracked_entries(
    excluded_paths: set[str], repo_root: Path = _REPO_ROOT
) -> list[tuple[int, str]]:
    """Returns (added lines, path) for untracked, non-ignored files not in excluded_paths.

    Raises:
        HandledError: If git cannot list the untracked files.
    """
    return [
        (count_added_lines(repo_root / path), path)
        for path in list_untracked_files(repo_root)
        if path not in excluded_paths
    ]


def _run_numstat(diff_spec: str | None) -> str:
    """Returns the stdout of `git diff --numstat [diff_spec]`."""
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
    return res.stdout


def get_diff_entries(diff_spec: str | None = None) -> list[tuple[int, str]]:
    """Extracts added line counts and filepaths from git diff plus untracked files.

    Untracked files that git does not ignore count every line as added, so the size gate
    holds before new files are committed. Paths already in the diff are not counted twice.
    """
    entries: list[tuple[int, str]] = []
    diff_paths: set[str] = set()
    for line in _run_numstat(diff_spec).splitlines():
        parts = line.strip().split("\t")
        if len(parts) < 3:
            continue
        diff_paths.add(parts[2])
        if parts[0].isdigit():
            entries.append((int(parts[0]), parts[2]))

    return entries + get_untracked_entries(diff_paths)


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
