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
references/xmc_docs_topography_map.json` and `git ls-files`:
1. DEAD_MAP_ENTRY (Map -> Repo): Every path/glob in `quadrants`
   (`source_patterns`, `reference_doc_patterns`, and `orientation_doc_patterns`)
   and `identifier_sources` (`source_patterns`) matches at least one tracked
   file.
2. UNMAPPED_DOC_FILE (Repo Docs -> Map): Every tracked `.md` file matches at
   least one documentation pattern in `quadrants[*].reference_doc_patterns` or
   `quadrants[*].orientation_doc_patterns`.
3. UNMAPPED_SOURCE_DIR (Repo Code -> Map): Every top-level component directory
   under `cmd/*/`, `sdks/*/*/`, `deploy/*/`, `pkg/*/`, `internal/*/`, `api/*/`,
   `examples/*/`, and `tools/*/` has at least one tracked file matched by a
   source pattern in `quadrants[*].source_patterns`.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from fnmatch import fnmatchcase
from pathlib import Path, PurePosixPath

DEFAULT_MAP_PATH = Path(
    ".agents/skills/xmc_review_docs_drift/references/xmc_docs_topography_map.json"
)

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


@dataclass(frozen=True)
class TopographyPatterns:
    """Extracted path and glob patterns from the JSON topography map."""

    source_patterns: tuple[str, ...]
    reference_doc_patterns: tuple[str, ...]
    orientation_doc_patterns: tuple[str, ...]
    registry_patterns: tuple[str, ...]

    @property
    def doc_patterns(self) -> tuple[str, ...]:
        """Returns all authoritative documentation patterns across both tiers."""
        return (*self.reference_doc_patterns, *self.orientation_doc_patterns)


@dataclass(frozen=True)
class ValidationReport:
    """Results of running the three bi-directional topography checks."""

    dead_map_entries: tuple[str, ...]
    unmapped_doc_files: tuple[str, ...]
    unmapped_source_dirs: tuple[str, ...]

    def is_valid(self) -> bool:
        """Returns True when all three bi-directional checks pass."""
        return not (self.dead_map_entries or self.unmapped_doc_files or self.unmapped_source_dirs)


def _match_parts(path_parts: tuple[str, ...], pat_parts: tuple[str, ...]) -> bool:
    """Matches path segments against glob segments supporting `**`."""
    if not pat_parts:
        return not path_parts
    if pat_parts[0] == "**":
        return _match_parts(path_parts, pat_parts[1:]) or (
            bool(path_parts) and _match_parts(path_parts[1:], pat_parts)
        )
    if not path_parts:
        return False
    return fnmatchcase(path_parts[0], pat_parts[0]) and _match_parts(path_parts[1:], pat_parts[1:])


def matches_pattern(file_path: str, pattern: str) -> bool:
    """Returns True if `file_path` matches the glob `pattern`."""
    return _match_parts(PurePosixPath(file_path).parts, PurePosixPath(pattern).parts)


def _validate_quadrant_doc_tiers(quadrant: dict[str, object]) -> None:
    """Validates that a quadrant defines reference/orientation doc patterns."""
    name = str(quadrant.get("name", "<unnamed>"))
    if "doc_patterns" in quadrant:
        raise ValueError(
            f"Quadrant '{name}' must split 'doc_patterns' into "
            "'reference_doc_patterns' and 'orientation_doc_patterns'."
        )
    ref_docs = quadrant.get("reference_doc_patterns", [])
    orient_docs = quadrant.get("orientation_doc_patterns", [])
    if not ref_docs and not orient_docs:
        raise ValueError(
            f"Quadrant '{name}' must define at least one pattern in "
            "'reference_doc_patterns' or 'orientation_doc_patterns'."
        )


def load_topography_map(json_text: str) -> TopographyPatterns:
    """Parses the JSON topography map into `TopographyPatterns`."""
    payload = json.loads(json_text)
    if not isinstance(payload, dict):
        raise ValueError("Topography map JSON root must be an object.")
    quadrants = payload.get("quadrants", [])
    identifier_sources = payload.get("identifier_sources", [])
    for quadrant in quadrants:
        _validate_quadrant_doc_tiers(quadrant)
    source_patterns = [
        str(pattern) for quadrant in quadrants for pattern in quadrant.get("source_patterns", [])
    ]
    reference_doc_patterns = [
        str(pattern)
        for quadrant in quadrants
        for pattern in quadrant.get("reference_doc_patterns", [])
    ]
    orientation_doc_patterns = [
        str(pattern)
        for quadrant in quadrants
        for pattern in quadrant.get("orientation_doc_patterns", [])
    ]
    registry_patterns = [
        str(pattern) for entry in identifier_sources for pattern in entry.get("source_patterns", [])
    ]
    return TopographyPatterns(
        source_patterns=tuple(source_patterns),
        reference_doc_patterns=tuple(reference_doc_patterns),
        orientation_doc_patterns=tuple(orientation_doc_patterns),
        registry_patterns=tuple(registry_patterns),
    )


def discover_component_directories(tracked_files: Sequence[str]) -> list[str]:
    """Returns sorted top-level component directories containing tracked files."""
    directories: set[str] = set()
    for file_path in tracked_files:
        parts = PurePosixPath(file_path).parts
        if len(parts) >= 3 and parts[0] in SINGLE_LEVEL_COMPONENT_ROOTS:
            directories.add(f"{parts[0]}/{parts[1]}/")
        if len(parts) >= 4 and parts[0] in TWO_LEVEL_COMPONENT_ROOTS:
            directories.add(f"{parts[0]}/{parts[1]}/{parts[2]}/")
    return sorted(directories)


def find_dead_map_entries(
    patterns: TopographyPatterns, tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns map patterns that match zero tracked files in the repository."""
    unique_patterns = dict.fromkeys(
        (
            *patterns.source_patterns,
            *patterns.doc_patterns,
            *patterns.registry_patterns,
        )
    )
    return tuple(
        pattern
        for pattern in unique_patterns
        if not any(matches_pattern(path, pattern) for path in tracked_files)
    )


def find_unmapped_doc_files(
    doc_patterns: Sequence[str], tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns tracked `.md` files not covered by any `doc_patterns` entry."""
    markdown_files = sorted(path for path in tracked_files if path.endswith(".md"))
    return tuple(
        md_file
        for md_file in markdown_files
        if not any(matches_pattern(md_file, pattern) for pattern in doc_patterns)
    )


def find_unmapped_source_dirs(
    source_patterns: Sequence[str], tracked_files: Sequence[str]
) -> tuple[str, ...]:
    """Returns top-level component directories with no mapped tracked files."""
    return tuple(
        comp_dir
        for comp_dir in discover_component_directories(tracked_files)
        if not any(
            matches_pattern(path, pattern)
            for path in tracked_files
            if path.startswith(comp_dir)
            for pattern in source_patterns
        )
    )


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
        f"[UNMAPPED_DOC_FILE] Tracked Markdown file not covered by doc_patterns: `{doc}`"
        for doc in report.unmapped_doc_files
    )
    lines.extend(
        f"[UNMAPPED_SOURCE_DIR] Component directory not covered by source_patterns: `{src_dir}`"
        for src_dir in report.unmapped_source_dirs
    )
    return lines


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """Parses command-line arguments for the topography map linter."""
    parser = argparse.ArgumentParser(
        description="Validate XMC Documentation Topography Map JSON against git ls-files."
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
        help="Path to xmc_docs_topography_map.json relative to repo root.",
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

    patterns = load_topography_map(map_path.read_text(encoding="utf-8"))
    tracked_files = list_tracked_files(args.repo_root)
    report = validate_topography_map(patterns, tracked_files)
    if report.is_valid():
        return 0

    for line in format_failures(report):
        print(line, file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
