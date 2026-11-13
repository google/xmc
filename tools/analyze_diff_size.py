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

# Bazel build definitions are reported separately and excluded from code totals.
# These are compiled case-sensitively so lowercase `build` scripts stay in prod.
BUILD_PATTERNS = (
    r"(^|.*/)BUILD(\.bazel)?$",
    r".*\.bzl$",
)

# English words ending in "test" that are not Go-style `<pkg>test` helper dirs.
_NON_HELPER_TEST_DIRS = r"(?:latest|attest|contest|protest|detest|greatest)"

TEST_PATTERNS = (
    r".*(_test|\.spec|\.test)\.(go|py|ts|js)$",
    r"(^|.*/)(test|tests|testing|testdouble)/.*",
    # Go-style test-helper packages such as `prototest/` or `experimenttest/`.
    rf"(^|.*/)(?!{_NON_HELPER_TEST_DIRS}/)[a-z0-9]+test/.*",
    r".*(fixture|fake|mock).*\.(go|py|ts|js)$",
)

DOC_PATTERNS = (
    r".*\.(md|markdown|rst)$",
    r"(^|.*/)docs/.*",
)

# Production roots whose behavior changes usually require documentation updates.
DOC_SYNC_PROD_ROOTS = ("api/", "cmd/", "deploy/", "internal/", "pkg/")


def _compile_all(patterns: tuple[str, ...], flags: int = 0) -> tuple[re.Pattern[str], ...]:
    return tuple(re.compile(p, flags) for p in patterns)


# Evaluated in order; the first matching category wins.
_ORDERED_CATEGORIES = (
    ("generated", _compile_all(GENERATED_PATTERNS, re.IGNORECASE)),
    ("build", _compile_all(BUILD_PATTERNS)),
    ("doc", _compile_all(DOC_PATTERNS, re.IGNORECASE)),
    ("test", _compile_all(TEST_PATTERNS, re.IGNORECASE)),
)


def classify_file(file_path: str) -> str:
    """Classifies a relative path as 'generated', 'build', 'doc', 'test', or 'prod'."""
    normalized = file_path.replace("\\", "/").strip()
    for category, patterns in _ORDERED_CATEGORIES:
        if any(p.match(normalized) for p in patterns):
            return category
    return "prod"


def count_added_lines(file_path: Path) -> int:
    """Returns the lines git numstat would report as added for a new file.

    Mirrors git's binary heuristic (a NUL byte in the first 8000 bytes): binary or
    unreadable files count 0, like numstat's '-'. A final line without a newline counts.
    The file is streamed in fixed-size chunks, so memory use stays bounded. A symlink
    counts 1 (git diffs its target string), and other non-regular files (FIFOs,
    devices) count 0 so reading them can never block.
    """
    if file_path.is_symlink():
        return 1
    if not file_path.is_file():
        return 0
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


def get_untracked_entries(excluded_paths: set[str], repo_root: Path) -> list[tuple[int, str]]:
    """Returns (added lines, path) for untracked, non-ignored files not in excluded_paths.

    Raises:
        HandledError: If git cannot list the untracked files.
    """
    return [
        (count_added_lines(repo_root / path), path)
        for path in list_untracked_files(repo_root)
        if path not in excluded_paths
    ]


def _find_repo_root() -> Path:
    """Returns the top-level directory of the git checkout containing the working directory.

    Raises:
        HandledError: If the working directory is not inside a git checkout.
    """
    cmd = ["git", "rev-parse", "--show-toplevel"]
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Failed to find the git repository root: {e.stderr.strip()}",
            "Verify that you are inside a valid git workspace.",
        ) from e
    return Path(res.stdout.strip())


def _run_numstat(diff_spec: str | None, repo_root: Path) -> str:
    """Returns the stdout of `git diff --numstat [diff_spec]` run in repo_root.

    core.quotePath=false keeps non-ASCII paths unescaped, matching `git ls-files -z`.
    """
    cmd = ["git", "-c", "core.quotePath=false", "diff", "--numstat"]
    if diff_spec:
        cmd.append(diff_spec)

    try:
        res = subprocess.run(cmd, capture_output=True, text=True, check=True, cwd=repo_root)
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
    Both listings run from the root of the checkout containing the working directory, so
    their repository-relative paths match.
    """
    repo_root = _find_repo_root()
    entries: list[tuple[int, str]] = []
    diff_paths: set[str] = set()
    for line in _run_numstat(diff_spec, repo_root).splitlines():
        parts = line.strip().split("\t")
        if len(parts) < 3:
            continue
        diff_paths.add(parts[2])
        if parts[0].isdigit():
            entries.append((int(parts[0]), parts[2]))

    return entries + get_untracked_entries(diff_paths, repo_root)


def tally_lines(entries: list[tuple[int, str]]) -> dict[str, int]:
    """Sums added lines per category, plus prod lines under DOC_SYNC_PROD_ROOTS."""
    totals = dict.fromkeys(("prod", "test", "doc", "generated", "build", "doc_sync_prod"), 0)
    for added, path in entries:
        category = classify_file(path)
        totals[category] += added
        if category == "prod" and path.startswith(DOC_SYNC_PROD_ROOTS):
            totals["doc_sync_prod"] += added
    return totals


def doc_sync_warnings(totals: dict[str, int]) -> list[str]:
    """Returns an advisory warning when documented roots change without doc updates."""
    if totals["doc_sync_prod"] == 0 or totals["doc"] > 0:
        return []
    roots = ", ".join(DOC_SYNC_PROD_ROOTS)
    return [
        f"Production code under {roots} changed ({totals['doc_sync_prod']} lines) "
        "with no documentation lines. If behavior, APIs, flags, or config changed, update "
        "docs/flows/*.md, the affected binary/component/profile README.md, and "
        "docs/migrations/*.md in the same PR; otherwise state why in the PR body."
    ]


def exceeded_threshold_reasons(prod: int, total_code: int, doc: int) -> list[str]:
    """Lists every sizing threshold that the diff meets or exceeds."""
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
    return exceeded_reasons


def analyze_diff_size(args: argparse.Namespace) -> dict[str, Any]:
    """Evaluates diff size against mandatory PR sizing thresholds."""
    totals = tally_lines(get_diff_entries(args.base_ref))
    total_code = totals["prod"] + totals["test"]
    exceeded_reasons = exceeded_threshold_reasons(totals["prod"], total_code, totals["doc"])
    should_split = len(exceeded_reasons) > 0
    return {
        "thresholds": {
            "prod_code": PROD_THRESHOLD,
            "total_code": TOTAL_CODE_THRESHOLD,
            "docs": DOC_THRESHOLD,
        },
        "metrics": {
            "prod_code_lines": totals["prod"],
            "test_code_lines": totals["test"],
            "total_code_lines": total_code,
            "doc_lines": totals["doc"],
            "generated_lines": totals["generated"],
            "build_lines": totals["build"],
        },
        "threshold_exceeded": should_split,
        "exceeded_reasons": exceeded_reasons,
        "should_trigger_split_verifier": should_split,
        "warnings": doc_sync_warnings(totals),
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
