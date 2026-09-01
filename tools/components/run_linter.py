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

"""Runner script for discovering and validating XMC component manifests."""

import argparse
import json
import os
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

DEFAULT_SCAN_ROOTS = (
    "deploy/profiles",
    "deploy/components",
    "examples/components/profiles",
)


@dataclass(frozen=True)
class LintTarget:
    """Represents a discovered component manifest directory and its rules file."""

    manifests_dir: Path
    rules_config: Path | None = None


@dataclass(frozen=True)
class LintResult:
    """Represents the execution result of linting a single manifest directory."""

    manifests_dir: str
    rules_config: str | None
    success: bool
    returncode: int
    stdout: str
    stderr: str
    json_data: dict[str, Any] | None = None


def get_base_directory() -> Path:
    """Returns the execution base directory or repository root."""
    if "BUILD_WORKING_DIRECTORY" in os.environ:
        return Path(os.environ["BUILD_WORKING_DIRECTORY"]).resolve()
    return Path.cwd().resolve()


def resolve_linter_binary(
    linter_binary: str | None,
    base_dir: Path | None = None,
) -> str | None:
    """Resolves the linter binary path or pre-built bazel-bin executable if available."""
    if linter_binary:
        return linter_binary
    base = base_dir if base_dir is not None else get_base_directory()
    candidates = [
        base / "bazel-bin" / "cmd" / "xmc-linter" / "xmc-linter_" / "xmc-linter",
        base / "bazel-bin" / "cmd" / "xmc-linter" / "xmc-linter",
    ]
    for cand in candidates:
        if cand.is_file() and os.access(cand, os.X_OK):
            return str(cand)
    return None


def normalize_scan_roots(
    roots: Sequence[str],
    base_dir: Path | None = None,
) -> list[Path]:
    """Normalizes scan roots, expanding comma-separated values and resolving relative paths."""
    base = base_dir if base_dir is not None else get_base_directory()
    normalized: list[Path] = []
    for root in roots:
        for part in root.split(","):
            cleaned = part.strip()
            if not cleaned:
                continue
            path = Path(cleaned)
            if not path.is_absolute():
                path = (base / path).resolve()
            normalized.append(path)
    return normalized


def find_manifest_files(directory: Path) -> list[Path]:
    """Finds all YAML manifest files directly in the given directory (excluding rules files)."""
    if not directory.is_dir():
        return []
    manifests: list[Path] = []
    try:
        for item in directory.iterdir():
            if item.is_file() and item.suffix.lower() in (".yaml", ".yml"):
                if item.name.lower() not in ("rules.yaml", "rules.yml"):
                    manifests.append(item)
    except OSError:
        pass
    return sorted(manifests)


def find_rules_config(directory: Path) -> Path | None:
    """Finds a co-located rules.yaml or rules.yml file if present."""
    for candidate in ("rules.yaml", "rules.yml"):
        rules_file = directory / candidate
        if rules_file.is_file():
            return rules_file
    return None


def discover_lint_targets(scan_roots: Sequence[Path]) -> list[LintTarget]:
    """Discovers component manifest directories and co-located rules across scan roots."""
    discovered: dict[Path, LintTarget] = {}
    for root in scan_roots:
        if not root.exists() or not root.is_dir():
            continue
        for current_dir, dirnames, filenames in os.walk(root):
            dirnames.sort()
            dir_path = Path(current_dir)
            has_manifests = any(
                f.lower().endswith((".yaml", ".yml"))
                and f.lower() not in ("rules.yaml", "rules.yml")
                for f in filenames
            )
            if has_manifests:
                resolved = dir_path.resolve()
                if resolved not in discovered:
                    discovered[resolved] = LintTarget(
                        manifests_dir=dir_path,
                        rules_config=find_rules_config(dir_path),
                    )
    return sorted(discovered.values(), key=lambda t: str(t.manifests_dir))


def build_linter_command(
    target: LintTarget,
    linter_binary: str | None,
    output_format: str,
) -> list[str]:
    """Constructs the command arguments to execute xmc-linter for a target."""
    flags = [
        f"--manifests-dir={target.manifests_dir}",
        f"--output={output_format}",
    ]
    if target.rules_config:
        flags.append(f"--rules-config={target.rules_config}")

    if linter_binary:
        return [linter_binary, *flags]
    return ["bazel", "run", "//cmd/xmc-linter:xmc-linter", "--", *flags]


def run_linter_on_target(
    target: LintTarget,
    linter_binary: str | None,
    output_format: str,
    cwd: Path | None = None,
) -> LintResult:
    """Executes the xmc-linter against a single lint target directory."""
    work_dir = cwd if cwd is not None else get_base_directory()
    cmd = build_linter_command(target, linter_binary, output_format)
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            check=False,
            cwd=str(work_dir),
            encoding="utf-8",
        )
    except OSError as e:
        return LintResult(
            manifests_dir=str(target.manifests_dir),
            rules_config=str(target.rules_config) if target.rules_config else None,
            success=False,
            returncode=-1,
            stdout="",
            stderr=f"Failed to execute linter command: {e}",
        )

    json_data: dict[str, Any] | None = None
    if output_format == "json" and proc.stdout.strip():
        try:
            json_data = json.loads(proc.stdout.strip())
        except json.JSONDecodeError:
            json_data = None

    return LintResult(
        manifests_dir=str(target.manifests_dir),
        rules_config=str(target.rules_config) if target.rules_config else None,
        success=(proc.returncode == 0),
        returncode=proc.returncode,
        stdout=proc.stdout,
        stderr=proc.stderr,
        json_data=json_data,
    )


def run_all_linter_targets(
    targets: Sequence[LintTarget],
    linter_binary: str | None,
    output_format: str,
    cwd: Path | None = None,
) -> list[LintResult]:
    """Executes validation sequentially across all discovered targets."""
    resolved_binary = resolve_linter_binary(linter_binary, base_dir=cwd)
    return [
        run_linter_on_target(target, resolved_binary, output_format, cwd=cwd) for target in targets
    ]


def format_text_report(results: Sequence[LintResult]) -> tuple[str, bool]:
    """Formats aggregated lint results into human-readable text output."""
    lines: list[str] = []
    passed = 0
    failed = 0

    for res in results:
        rules_info = f" (rules: {res.rules_config})" if res.rules_config else ""
        lines.append(f"=== Validating Profile: {res.manifests_dir}{rules_info} ===")
        if res.stdout.strip():
            lines.append(res.stdout.strip())
        if res.stderr.strip():
            lines.append(res.stderr.strip())
        if res.success:
            passed += 1
        else:
            failed += 1
        lines.append("")

    total = len(results)
    lines.append(
        f"=== Component Linting Summary: {total} total, {passed} passed, {failed} failed ==="
    )
    return "\n".join(lines), failed == 0


def format_json_report(
    results: Sequence[LintResult],
    notice: str | None = None,
) -> dict[str, Any]:
    """Formats aggregated lint results into a structured dictionary for JSON output."""
    passed = sum(1 for r in results if r.success)
    failed = sum(1 for r in results if not r.success)
    all_valid = failed == 0

    serialized_results: list[dict[str, Any]] = []
    for r in results:
        entry: dict[str, Any] = {
            "manifests_dir": r.manifests_dir,
            "rules_config": r.rules_config,
            "valid": r.success,
            "returncode": r.returncode,
        }
        if r.json_data is not None:
            entry["result"] = r.json_data
        else:
            entry["stdout"] = r.stdout
            entry["stderr"] = r.stderr
        serialized_results.append(entry)

    report: dict[str, Any] = {
        "valid": all_valid,
        "total_targets": len(results),
        "passed_targets": passed,
        "failed_targets": failed,
        "results": serialized_results,
    }
    if notice:
        report["notice"] = notice
    return report


def build_arg_parser() -> argparse.ArgumentParser:
    """Constructs the CLI argument parser for run_linter."""
    parser = argparse.ArgumentParser(
        description="Discover and run static validation across XMC component profiles.",
    )
    parser.add_argument(
        "--scan-roots",
        nargs="*",
        default=list(DEFAULT_SCAN_ROOTS),
        help="Directory roots to scan for component profiles (default: standard profile paths).",
    )
    parser.add_argument(
        "--linter-binary",
        default=None,
        help="Path to pre-built xmc-linter executable (optional, falls back to 'bazel run').",
    )
    parser.add_argument(
        "--output",
        choices=["text", "json"],
        default="text",
        help="Output reporting format (text or json, default: text).",
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help="Fail with non-zero exit code if no profile directories are discovered.",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """Main CLI entrypoint for running the XMC component manifests linter."""
    parser = build_arg_parser()
    args = parser.parse_args(argv)

    scan_roots = normalize_scan_roots(args.scan_roots)
    targets = discover_lint_targets(scan_roots)

    if not targets:
        if args.strict:
            msg = "Error: No profile or component manifest directories discovered in strict mode."
            if args.output == "json":
                print(json.dumps({"valid": False, "error": msg, "total_targets": 0}, indent=2))
            else:
                print(f"[ERROR] {msg}", file=sys.stderr)
            return 1

        notice = "Notice: No profile or component manifest directories found under scan roots."
        if args.output == "json":
            print(json.dumps(format_json_report([], notice=notice), indent=2))
        else:
            print(f"[INFO] {notice}")
        return 0

    results = run_all_linter_targets(targets, args.linter_binary, args.output)

    if args.output == "json":
        report_data = format_json_report(results)
        print(json.dumps(report_data, indent=2))
        return 0 if report_data["valid"] else 1

    report_text, all_valid = format_text_report(results)
    print(report_text)
    return 0 if all_valid else 1


if __name__ == "__main__":
    sys.exit(main())
