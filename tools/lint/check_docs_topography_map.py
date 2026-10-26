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

"""Bi-directional linter for the XMC Documentation Topography Map.

Enforces three invariants between `.agents/skills/xmc_review_docs_drift/
references/xmc_docs_topography_and_map.md` and `git ls-files`:
1. DEAD_MAP_ENTRY (Map -> Repo): Every path/glob in Section 1 (Columns 2 & 3)
   and Section 2 (Column 2) matches at least one tracked file.
2. UNMAPPED_DOC_FILE (Repo Docs -> Map): Every tracked `.md` file matches at
   least one authoritative documentation pattern in Section 1 Column 3.
3. UNMAPPED_SOURCE_DIR (Repo Code -> Map): Every top-level component directory
   under `cmd/*/`, `sdks/*/*/`, `deploy/*/`, `pkg/*/`, `internal/*/`, `api/*/`,
   `examples/*/`, and `tools/*/` has at least one tracked file matched by a
   source pattern in Section 1 Column 2.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

DEFAULT_MAP_PATH = Path(
    ".agents/skills/xmc_review_docs_drift/references/xmc_docs_topography_and_map.md"
)
SECTION_1_HEADER = "## 1. Code-to-Docs Topography Mapping Matrix"
SECTION_2_HEADER = "## 2. Identifier Sources of Truth Registry"

SINGLE_LEVEL_COMPONENT_ROOTS: tuple[str, ...] = (
    "cmd",
    "deploy",
    "pkg",
    "internal",
    "api",
    "examples",
    "tools",
)
TWO_LEVEL_COMPONENT_ROOTS: tuple[str, ...] = ("sdks",)

_BACKTICK_TOKEN_RE = re.compile(r"`([^`]+)`")
_PLACEHOLDER_RE = re.compile(r"<[^/>]+>")
_GLOB_TOKEN_RE = re.compile(r"/\*\*/|\*\*/|/\*\*|\*\*|\*|\?|[^/*?]+|/")


@dataclass(frozen=True)
class TopographyPatterns:
    """Extracted path and glob patterns from the topography map document."""

    source_patterns: tuple[str, ...]
    doc_patterns: tuple[str, ...]
    registry_patterns: tuple[str, ...]


@dataclass(frozen=True)
class ValidationReport:
    """Results of running the three bi-directional topography checks."""

    dead_map_entries: tuple[str, ...]
    unmapped_doc_files: tuple[str, ...]
    unmapped_source_dirs: tuple[str, ...]

    def is_valid(self) -> bool:
        """Returns True when all three bi-directional checks pass."""
        return not (self.dead_map_entries or self.unmapped_doc_files or self.unmapped_source_dirs)


def normalize_pattern(raw_pattern: str) -> str:
    """Normalizes `<placeholder>` segments to `*` and strips whitespace."""
    return _PLACEHOLDER_RE.sub("*", raw_pattern.strip())


def _translate_glob_token(token: str) -> str:
    """Translates a single glob token into its regular expression fragment."""
    token_map = {
        "/**/": r"(?:/.*)?/",
        "**/": r"(?:.*/)?",
        "/**": r"/.*",
        "**": r".*",
        "*": r"[^/]*",
        "?": r"[^/]",
    }
    if token in token_map:
        return token_map[token]
    return re.escape(token)


def compile_glob_regex(raw_pattern: str) -> re.Pattern[str]:
    """Compiles a repository path or glob pattern into a full-path regex."""
    normalized = normalize_pattern(raw_pattern)
    if normalized.endswith("/"):
        normalized = normalized[:-1] if normalized.endswith("/**/") else f"{normalized}**"
    fragments = [_translate_glob_token(tok) for tok in _GLOB_TOKEN_RE.findall(normalized)]
    return re.compile(f"^{''.join(fragments)}$")


def extract_backtick_tokens(cell_text: str) -> list[str]:
    """Extracts all backtick-delimited strings from a Markdown table cell."""
    return [match.strip() for match in _BACKTICK_TOKEN_RE.findall(cell_text) if match.strip()]


def extract_section_body(markdown_text: str, section_header: str) -> str:
    """Returns the Markdown text under `section_header` up to the next H2."""
    header_index = markdown_text.find(section_header)
    if header_index == -1:
        raise ValueError(f"Missing required section header: {section_header}")
    body_start = header_index + len(section_header)
    next_h2 = re.search(r"(?m)^##\s+", markdown_text[body_start:])
    if next_h2 is None:
        return markdown_text[body_start:]
    return markdown_text[body_start : body_start + next_h2.start()]


def parse_markdown_table_rows(section_text: str) -> list[list[str]]:
    """Extracts data cells from Markdown table rows in `section_text`."""
    table_lines = [
        line.strip() for line in section_text.splitlines() if line.strip().startswith("|")
    ]
    if len(table_lines) <= 2:
        return []
    rows: list[list[str]] = []
    for line in table_lines[2:]:
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        rows.append(cells)
    return rows


def parse_topography_map(markdown_text: str) -> TopographyPatterns:
    """Parses Section 1 and Section 2 tables into `TopographyPatterns`."""
    sec1_rows = parse_markdown_table_rows(extract_section_body(markdown_text, SECTION_1_HEADER))
    sec2_rows = parse_markdown_table_rows(extract_section_body(markdown_text, SECTION_2_HEADER))
    source_patterns = [
        tok for row in sec1_rows if len(row) >= 3 for tok in extract_backtick_tokens(row[1])
    ]
    doc_patterns = [
        tok for row in sec1_rows if len(row) >= 3 for tok in extract_backtick_tokens(row[2])
    ]
    registry_patterns = [
        tok for row in sec2_rows if len(row) >= 2 for tok in extract_backtick_tokens(row[1])
    ]
    return TopographyPatterns(
        source_patterns=tuple(source_patterns),
        doc_patterns=tuple(doc_patterns),
        registry_patterns=tuple(registry_patterns),
    )


def discover_component_directories(tracked_files: Sequence[str]) -> list[str]:
    """Returns sorted top-level component directories containing tracked files."""
    directories: set[str] = set()
    for file_path in tracked_files:
        parts = file_path.split("/")
        if len(parts) >= 3 and parts[0] in SINGLE_LEVEL_COMPONENT_ROOTS:
            directories.add(f"{parts[0]}/{parts[1]}/")
        if len(parts) >= 4 and parts[0] in TWO_LEVEL_COMPONENT_ROOTS:
            directories.add(f"{parts[0]}/{parts[1]}/{parts[2]}/")
    return sorted(directories)


def find_dead_map_entries(
    patterns: TopographyPatterns, tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns map patterns that match zero tracked files in the repository."""
    all_patterns = (
        *patterns.source_patterns,
        *patterns.doc_patterns,
        *patterns.registry_patterns,
    )
    seen: set[str] = set()
    dead: list[str] = []
    for pattern in all_patterns:
        if pattern in seen:
            continue
        seen.add(pattern)
        regex = compile_glob_regex(pattern)
        if not any(regex.match(path) for path in tracked_files):
            dead.append(pattern)
    return tuple(dead)


def find_unmapped_doc_files(
    doc_patterns: Sequence[str], tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns tracked `.md` files not covered by any Section 1 Col 3 pattern."""
    compiled = [compile_glob_regex(pattern) for pattern in doc_patterns]
    markdown_files = sorted(path for path in tracked_files if path.endswith(".md"))
    return tuple(
        md_file for md_file in markdown_files if not any(regex.match(md_file) for regex in compiled)
    )


def find_unmapped_source_dirs(
    source_patterns: Sequence[str], tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns top-level component directories with no mapped tracked files."""
    compiled = [compile_glob_regex(pattern) for pattern in source_patterns]
    component_dirs = discover_component_directories(tracked_files)
    unmapped: list[str] = []
    for comp_dir in component_dirs:
        dir_files = [path for path in tracked_files if path.startswith(comp_dir)]
        if not any(regex.match(path) for path in dir_files for regex in compiled):
            unmapped.append(comp_dir)
    return tuple(unmapped)


def validate_topography_map(
    patterns: TopographyPatterns, tracked_files: Sequence[str]
) -> ValidationReport:
    """Runs all three bi-directional checks on `patterns` and `tracked_files`."""
    return ValidationReport(
        dead_map_entries=find_dead_map_entries(patterns, tracked_files),
        unmapped_doc_files=find_unmapped_doc_files(patterns.doc_patterns, tracked_files),
        unmapped_source_dirs=find_unmapped_source_dirs(patterns.source_patterns, tracked_files),
    )


def list_tracked_files(repo_root: Path) -> list[str]:
    """Returns all tracked repository file paths via `git ls-files`."""
    result = subprocess.run(
        ["git", "ls-files"],
        cwd=repo_root,
        check=True,
        capture_output=True,
        text=True,
    )
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def format_failures(report: ValidationReport) -> list[str]:
    """Formats human-readable error lines for each failed topography check."""
    lines = [
        f"[DEAD_MAP_ENTRY] Pattern matches 0 tracked files: `{entry}`"
        for entry in report.dead_map_entries
    ]
    lines.extend(
        f"[UNMAPPED_DOC_FILE] Tracked Markdown file not covered by Section 1 Col 3: `{doc}`"
        for doc in report.unmapped_doc_files
    )
    lines.extend(
        f"[UNMAPPED_SOURCE_DIR] Component directory not covered by Section 1 Col 2: `{src_dir}`"
        for src_dir in report.unmapped_source_dirs
    )
    return lines


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """Parses command-line arguments for the topography map linter."""
    parser = argparse.ArgumentParser(
        description="Validate XMC Documentation Topography Map against git ls-files."
    )
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path("."),
        help="Repository root directory.",
    )
    parser.add_argument(
        "--map-file",
        type=Path,
        default=DEFAULT_MAP_PATH,
        help="Path to xmc_docs_topography_and_map.md relative to repo root.",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entrypoint for checking documentation topography map coverage."""
    args = parse_args(argv)
    map_path = args.map_file if args.map_file.is_absolute() else args.repo_root / args.map_file
    if not map_path.is_file():
        print(
            f"[DEAD_MAP_ENTRY] Topography map file not found: {map_path}",
            file=sys.stderr,
        )
        return 1

    patterns = parse_topography_map(map_path.read_text(encoding="utf-8"))
    tracked_files = list_tracked_files(args.repo_root)
    report = validate_topography_map(patterns, tracked_files)
    if report.is_valid():
        return 0

    for line in format_failures(report):
        print(line, file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
