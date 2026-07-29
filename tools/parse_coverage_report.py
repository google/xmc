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

"""Deterministic LCOV coverage report parser helper script.

Parses standard LCOV coverage report files, calculates line coverage metrics,
filters by package/file, enforces minimum coverage thresholds, and outputs
structured JSON.
"""

import argparse
from pathlib import Path
from typing import Any

from tool_utils import HandledError, run_tool


def normalize_package_path(package: str) -> str:
    """Normalizes package path or Bazel package pattern to a clean relative path prefix."""
    pkg = package.strip()
    if pkg.startswith("//"):
        pkg = pkg[2:]
    if pkg.endswith("/..."):
        pkg = pkg[:-4]
    elif pkg.endswith("..."):
        pkg = pkg[:-3]
    pkg = pkg.strip("/")
    if pkg.startswith("./"):
        pkg = pkg[2:].strip("/")
    return pkg


def matches_package(sf_path: str, norm_pkg: str) -> bool:
    """Checks if source_file belongs to the normalized package directory prefix."""
    if not norm_pkg:
        return True
    sf = sf_path.strip()
    if sf.startswith("./"):
        sf = sf[2:]
    if sf.startswith("//"):
        sf = sf[2:]
    sf = sf.lstrip("/")

    return sf == norm_pkg or sf.startswith(norm_pkg + "/") or f"/{norm_pkg}/" in sf


def matches_target_file(sf_path: str, target_file: str) -> bool:
    """Checks if source_file matches target_file filter."""
    if not target_file:
        return True
    tf = target_file.strip()
    if tf.startswith("./"):
        tf = tf[2:]
    if tf.startswith("//"):
        tf = tf[2:]
    tf = tf.lstrip("/")

    sf = sf_path.strip()
    if sf.startswith("./"):
        sf = sf[2:]
    if sf.startswith("//"):
        sf = sf[2:]
    sf = sf.lstrip("/")

    return sf == tf or sf.endswith("/" + tf)


def _merge_file_record(file_map: dict[str, dict[int, int]], sf: str, lines: dict[int, int]) -> None:
    """Merges line counts for a source file record into the aggregated map."""
    if sf not in file_map:
        file_map[sf] = {}
    for line_num, count in lines.items():
        file_map[sf][line_num] = max(file_map[sf].get(line_num, 0), count)


def parse_lcov_content(content: str) -> dict[str, dict[int, int]]:
    """Parses raw LCOV text content into a map of source files to line counts."""
    file_map: dict[str, dict[int, int]] = {}
    current_sf: str | None = None
    current_lines: dict[int, int] = {}

    for raw_line in content.splitlines():
        line = raw_line.strip()
        if not line:
            continue
        if line.startswith("SF:"):
            current_sf = line[3:].strip()
            current_lines = {}
        elif line.startswith("DA:"):
            if current_sf is not None:
                parts = line[3:].split(",")
                if len(parts) >= 2:
                    try:
                        line_num = int(parts[0])
                        count = int(parts[1])
                        current_lines[line_num] = count
                    except ValueError:
                        pass
        elif line == "end_of_record":
            if current_sf is not None:
                _merge_file_record(file_map, current_sf, current_lines)
                current_sf = None
                current_lines = {}

    if current_sf is not None:
        _merge_file_record(file_map, current_sf, current_lines)

    return file_map


def calculate_file_metrics(sf_path: str, line_counts: dict[int, int]) -> dict[str, Any]:
    """Calculates coverage metrics and lists uncovered line numbers for a single file."""
    total_lines = len(line_counts)
    uncovered_lines_list = sorted(line_num for line_num, count in line_counts.items() if count == 0)
    uncovered_lines_count = len(uncovered_lines_list)
    covered_lines_count = total_lines - uncovered_lines_count
    coverage_percentage = (covered_lines_count / total_lines * 100.0) if total_lines > 0 else 0.0

    return {
        "file": sf_path,
        "total_lines": total_lines,
        "covered_lines": covered_lines_count,
        "uncovered_lines": uncovered_lines_count,
        "coverage_percentage": round(coverage_percentage, 2),
        "uncovered_line_numbers": uncovered_lines_list,
    }


def process_coverage_data(
    file_lines_map: dict[str, dict[int, int]],
    package_filter: str | None = None,
    target_file_filter: str | None = None,
) -> dict[str, Any]:
    """Filters source files and computes aggregated coverage metrics."""
    norm_pkg = normalize_package_path(package_filter) if package_filter else ""

    file_reports: list[dict[str, Any]] = []
    total_lines = 0
    covered_lines = 0
    uncovered_lines = 0

    for sf_path in sorted(file_lines_map.keys()):
        if package_filter and not matches_package(sf_path, norm_pkg):
            continue
        if target_file_filter and not matches_target_file(sf_path, target_file_filter):
            continue

        metrics = calculate_file_metrics(sf_path, file_lines_map[sf_path])
        file_reports.append(metrics)
        total_lines += metrics["total_lines"]
        covered_lines += metrics["covered_lines"]
        uncovered_lines += metrics["uncovered_lines"]

    overall_coverage = (covered_lines / total_lines * 100.0) if total_lines > 0 else 0.0

    return {
        "total_lines": total_lines,
        "covered_lines": covered_lines,
        "uncovered_lines": uncovered_lines,
        "coverage_percentage": round(overall_coverage, 2),
        "files": file_reports,
    }


def main(args: argparse.Namespace) -> dict[str, Any]:
    """Main execution function for parsing coverage report."""
    report_path = Path(args.report_file)
    if not report_path.is_file():
        raise HandledError(
            f"Coverage report file not found: {args.report_file}",
            "Ensure 'bazel coverage ...' ran successfully and generated the report file.",
        )

    try:
        content = report_path.read_text(encoding="utf-8")
    except Exception as e:
        raise HandledError(
            f"Failed to read coverage report file '{args.report_file}': {e}",
            "Check file permissions and format.",
        ) from e

    file_lines_map = parse_lcov_content(content)
    result = process_coverage_data(
        file_lines_map=file_lines_map,
        package_filter=args.package,
        target_file_filter=args.target_file,
    )
    result["report_file"] = str(report_path)

    if args.min_coverage is not None:
        min_cov = float(args.min_coverage)
        actual_cov = result["coverage_percentage"]
        if actual_cov < min_cov:
            raise HandledError(
                f"Total coverage ({actual_cov:.2f}%) is below minimum threshold ({min_cov:.2f}%).",
                "Add unit tests to cover missing lines listed in the coverage report.",
            )

    return result


def build_parser() -> argparse.ArgumentParser:
    """Builds and returns the command-line argument parser."""
    parser = argparse.ArgumentParser(
        description="Deterministic LCOV coverage report parser helper."
    )
    parser.add_argument(
        "--report-file",
        "-f",
        default="bazel-out/_coverage/_coverage_report.dat",
        help="Path to the LCOV report file (default: bazel-out/_coverage/_coverage_report.dat).",
    )
    parser.add_argument(
        "--package",
        "-p",
        default=None,
        help="Target package path or prefix to filter (e.g. pkg/auth/oauth2 or //pkg/auth/oauth2/...).",
    )
    parser.add_argument(
        "--target-file",
        default=None,
        help="Optional specific source file to filter.",
    )
    parser.add_argument(
        "--min-coverage",
        "-t",
        type=float,
        default=None,
        help="Optional minimum coverage percentage threshold (e.g. 80.0).",
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), main)
