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

"""Tool for analyzing GitHub Actions CI workflow run performance and generating scorecards."""

import argparse
import json
import math
import statistics
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402


def parse_iso_timestamp(timestamp_str: str) -> datetime:
    """Parses an ISO 8601 timestamp string into a UTC datetime object."""
    try:
        normalized = timestamp_str.replace("Z", "+00:00")
        parsed_dt = datetime.fromisoformat(normalized)
        if parsed_dt.tzinfo is None:
            parsed_dt = parsed_dt.replace(tzinfo=timezone.utc)
        return parsed_dt.astimezone(timezone.utc)
    except (ValueError, TypeError) as exc:
        raise HandledError(
            f"Invalid ISO timestamp format: '{timestamp_str}'.",
            "Provide a valid ISO 8601 timestamp (e.g. '2026-08-20T12:00:00Z').",
        ) from exc


def calculate_run_duration(run: dict[str, Any]) -> float | None:
    """Calculates workflow run wall-clock execution duration in seconds."""
    started_str = run.get("run_started_at") or run.get("created_at")
    updated_str = run.get("updated_at")
    if not started_str or not updated_str:
        return None
    try:
        start_dt = parse_iso_timestamp(started_str)
        end_dt = parse_iso_timestamp(updated_str)
        duration_seconds = (end_dt - start_dt).total_seconds()
        return duration_seconds if duration_seconds >= 0 else None
    except HandledError:
        return None


def fetch_workflow_runs(
    owner: str = "google",
    repo: str = "xmc",
    limit: int = 100,
) -> list[dict[str, Any]]:
    """Fetches GitHub Actions workflow runs using the gh CLI."""
    runs: list[dict[str, Any]] = []
    page = 1
    per_page = min(limit, 100)

    while len(runs) < limit:
        endpoint = f"repos/{owner}/{repo}/actions/runs?per_page={per_page}&page={page}"
        cmd = ["gh", "api", endpoint]
        try:
            res = subprocess.run(cmd, capture_output=True, text=True, check=True)
            data = json.loads(res.stdout)
        except subprocess.CalledProcessError as exc:
            err_msg = exc.stderr.strip() if exc.stderr else str(exc)
            raise HandledError(
                f"Failed to fetch workflow runs from GitHub: {err_msg}",
                "Ensure 'gh' CLI is authenticated and has read access to the repository.",
            ) from exc
        except json.JSONDecodeError as exc:
            raise HandledError(
                f"Invalid JSON response from GitHub API: {exc}",
                "Check GitHub API connectivity and response format.",
            ) from exc
        except OSError as exc:
            raise HandledError(
                f"Failed to execute gh CLI: {exc}",
                "Ensure gh CLI is installed and accessible in system PATH.",
            ) from exc

        batch = data.get("workflow_runs", [])
        if not batch:
            break
        runs.extend(batch)
        if len(batch) < per_page:
            break
        page += 1

    return runs[:limit]


def filter_runs(
    runs: list[dict[str, Any]],
    workflow_name: str | None = None,
    since_dt: datetime | None = None,
) -> list[dict[str, Any]]:
    """Filters workflow runs by workflow name and minimum start timestamp."""
    filtered: list[dict[str, Any]] = []
    for run in runs:
        if workflow_name and run.get("name") != workflow_name:
            continue
        started_str = run.get("run_started_at") or run.get("created_at")
        if not started_str:
            continue
        try:
            run_dt = parse_iso_timestamp(started_str)
        except HandledError:
            continue
        if since_dt and run_dt < since_dt:
            continue
        filtered.append(run)
    return filtered


def split_runs_by_compare_time(
    runs: list[dict[str, Any]],
    compare_dt: datetime,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """Splits runs into Before and After periods based on comparison timestamp."""
    before_runs: list[dict[str, Any]] = []
    after_runs: list[dict[str, Any]] = []
    for run in runs:
        started_str = run.get("run_started_at") or run.get("created_at")
        if not started_str:
            continue
        try:
            run_dt = parse_iso_timestamp(started_str)
        except HandledError:
            continue
        if run_dt < compare_dt:
            before_runs.append(run)
        else:
            after_runs.append(run)
    return before_runs, after_runs


def extract_completed_durations(runs: list[dict[str, Any]]) -> list[float]:
    """Extracts duration in seconds for all completed workflow runs."""
    durations: list[float] = []
    for run in runs:
        if run.get("status") == "completed":
            duration = calculate_run_duration(run)
            if duration is not None:
                durations.append(duration)
    return durations


def calculate_percentile(sorted_data: list[float], percentile: float) -> float:
    """Calculates percentile using linear interpolation on sorted float values."""
    if not sorted_data:
        return 0.0
    if len(sorted_data) == 1:
        return sorted_data[0]
    rank = (len(sorted_data) - 1) * percentile
    lower_idx = math.floor(rank)
    upper_idx = math.ceil(rank)
    if lower_idx == upper_idx:
        return sorted_data[int(rank)]
    weight = rank - lower_idx
    return sorted_data[lower_idx] * (1.0 - weight) + sorted_data[upper_idx] * weight


def calculate_metrics(durations: list[float]) -> dict[str, Any]:
    """Calculates aggregate statistical metrics for a list of run durations."""
    if not durations:
        return {
            "count": 0,
            "mean": 0.0,
            "median": 0.0,
            "p90": 0.0,
            "min": 0.0,
            "max": 0.0,
        }
    sorted_durations = sorted(durations)
    return {
        "count": len(sorted_durations),
        "mean": round(statistics.fmean(sorted_durations), 2),
        "median": round(statistics.median(sorted_durations), 2),
        "p90": round(calculate_percentile(sorted_durations, 0.90), 2),
        "min": round(min(sorted_durations), 2),
        "max": round(max(sorted_durations), 2),
    }


def calculate_comparative_deltas(
    before_metrics: dict[str, Any],
    after_metrics: dict[str, Any],
) -> dict[str, Any]:
    """Calculates absolute and percentage deltas between before and after metrics."""
    deltas: dict[str, Any] = {
        "count_delta": after_metrics["count"] - before_metrics["count"],
    }
    for key in ["median", "mean", "p90", "min", "max"]:
        before_val = before_metrics.get(key, 0.0)
        after_val = after_metrics.get(key, 0.0)
        delta_sec = round(after_val - before_val, 2)
        pct_delta = (
            round(((after_val - before_val) / before_val * 100.0), 2) if before_val > 0 else 0.0
        )
        deltas[f"{key}_delta_sec"] = delta_sec
        deltas[f"{key}_pct_delta"] = pct_delta
    return deltas


def format_duration(seconds: float) -> str:
    """Formats duration in seconds into a clean human-readable representation."""
    if seconds < 0:
        return f"-{format_duration(-seconds)}"
    if seconds < 60:
        return f"{seconds:.2f}s"
    minutes = int(seconds // 60)
    remaining_sec = seconds % 60
    return f"{minutes}m {remaining_sec:05.2f}s ({seconds:.2f}s)"


def generate_markdown_scorecard(summary: dict[str, Any]) -> str:
    """Generates a formatted GitHub Flavored Markdown scorecard table."""
    workflow = summary.get("workflow", "Workflow")
    owner = summary.get("owner", "google")
    repo = summary.get("repo", "xmc")

    lines = [
        "### ⏱️ CI Performance Scorecard",
        f"- **Repository**: `{owner}/{repo}`",
        f"- **Workflow**: `{workflow}`",
    ]

    if summary.get("is_comparison"):
        lines.extend(_format_comparison_scorecard_lines(summary))
    else:
        lines.extend(_format_single_scorecard_lines(summary))

    return "\n".join(lines)


def _format_comparison_scorecard_lines(summary: dict[str, Any]) -> list[str]:
    """Formats comparison Markdown table lines."""
    before = summary.get("before", {}).get("metrics", {})
    after = summary.get("after", {}).get("metrics", {})
    deltas = summary.get("deltas", {})
    compare_time = summary.get("compare_time", "N/A")

    median_pct = deltas.get("median_pct_delta", 0.0)
    median_sec = deltas.get("median_delta_sec", 0.0)
    if median_pct < 0:
        status_banner = f"🟢 **Speedup**: Median duration improved by {abs(median_pct):.2f}% ({abs(median_sec):.2f}s)"
    elif median_pct > 0:
        status_banner = f"🔴 **Regression**: Median duration increased by {median_pct:.2f}% (+{median_sec:.2f}s)"
    else:
        status_banner = "⚪ **Neutral**: Median duration remained unchanged"

    return [
        f"- **Compare Cutoff**: `{compare_time}`",
        f"- **Performance Verdict**: {status_banner}",
        "",
        "| Metric | Before | After | Delta (s) | Delta (%) |",
        "| :--- | :--- | :--- | :--- | :--- |",
        f"| **Runs Count** | {before.get('count', 0)} | {after.get('count', 0)} | {deltas.get('count_delta', 0):+d} | - |",
        f"| **Median Duration** | {format_duration(before.get('median', 0.0))} | {format_duration(after.get('median', 0.0))} | {deltas.get('median_delta_sec', 0.0):+.2f}s | {deltas.get('median_pct_delta', 0.0):+.2f}% |",
        f"| **Mean Duration** | {format_duration(before.get('mean', 0.0))} | {format_duration(after.get('mean', 0.0))} | {deltas.get('mean_delta_sec', 0.0):+.2f}s | {deltas.get('mean_pct_delta', 0.0):+.2f}% |",
        f"| **P90 Duration** | {format_duration(before.get('p90', 0.0))} | {format_duration(after.get('p90', 0.0))} | {deltas.get('p90_delta_sec', 0.0):+.2f}s | {deltas.get('p90_pct_delta', 0.0):+.2f}% |",
        f"| **Min Duration** | {format_duration(before.get('min', 0.0))} | {format_duration(after.get('min', 0.0))} | {deltas.get('min_delta_sec', 0.0):+.2f}s | {deltas.get('min_pct_delta', 0.0):+.2f}% |",
        f"| **Max Duration** | {format_duration(before.get('max', 0.0))} | {format_duration(after.get('max', 0.0))} | {deltas.get('max_delta_sec', 0.0):+.2f}s | {deltas.get('max_pct_delta', 0.0):+.2f}% |",
    ]


def _format_single_scorecard_lines(summary: dict[str, Any]) -> list[str]:
    """Formats single period Markdown table lines."""
    metrics = summary.get("metrics", {})
    return [
        "",
        "| Metric | Value |",
        "| :--- | :--- |",
        f"| **Runs Count** | {metrics.get('count', 0)} |",
        f"| **Median Duration** | {format_duration(metrics.get('median', 0.0))} |",
        f"| **Mean Duration** | {format_duration(metrics.get('mean', 0.0))} |",
        f"| **P90 Duration** | {format_duration(metrics.get('p90', 0.0))} |",
        f"| **Min Duration** | {format_duration(metrics.get('min', 0.0))} |",
        f"| **Max Duration** | {format_duration(metrics.get('max', 0.0))} |",
    ]


def analyze_ci_performance(args: argparse.Namespace) -> dict[str, Any]:
    """Orchestrates CI performance analysis, statistical calculations, and scorecard generation."""
    since_dt = None
    if args.since:
        since_dt = parse_iso_timestamp(args.since)
    elif args.hours is not None:
        if args.hours <= 0:
            raise HandledError(
                f"Invalid --hours argument: {args.hours}. Must be greater than 0.",
                "Specify a positive number of hours (e.g. --hours 24).",
            )
        since_dt = datetime.now(timezone.utc) - timedelta(hours=args.hours)

    compare_dt = parse_iso_timestamp(args.compare_time) if args.compare_time else None

    raw_runs = fetch_workflow_runs(owner=args.owner, repo=args.repo, limit=args.limit)
    matched_runs = filter_runs(runs=raw_runs, workflow_name=args.workflow, since_dt=since_dt)

    result_data: dict[str, Any] = {
        "workflow": args.workflow,
        "owner": args.owner,
        "repo": args.repo,
        "total_runs_fetched": len(raw_runs),
        "matched_runs_count": len(matched_runs),
        "is_comparison": compare_dt is not None,
    }

    if compare_dt:
        before_runs, after_runs = split_runs_by_compare_time(matched_runs, compare_dt)
        before_durations = extract_completed_durations(before_runs)
        after_durations = extract_completed_durations(after_runs)
        before_metrics = calculate_metrics(before_durations)
        after_metrics = calculate_metrics(after_durations)
        deltas = calculate_comparative_deltas(before_metrics, after_metrics)

        result_data["compare_time"] = args.compare_time
        result_data["before"] = {
            "total_runs": len(before_runs),
            "completed_runs": len(before_durations),
            "metrics": before_metrics,
        }
        result_data["after"] = {
            "total_runs": len(after_runs),
            "completed_runs": len(after_durations),
            "metrics": after_metrics,
        }
        result_data["deltas"] = deltas
    else:
        durations = extract_completed_durations(matched_runs)
        metrics = calculate_metrics(durations)
        result_data["completed_runs_count"] = len(durations)
        result_data["metrics"] = metrics

    markdown_scorecard = generate_markdown_scorecard(result_data)
    result_data["markdown_scorecard"] = markdown_scorecard

    if args.output:
        try:
            output_path = Path(args.output)
            output_path.parent.mkdir(parents=True, exist_ok=True)
            output_path.write_text(markdown_scorecard, encoding="utf-8")
            result_data["output_file"] = str(output_path)
        except OSError as exc:
            raise HandledError(
                f"Failed to write markdown output to '{args.output}': {exc}",
                "Check file path permissions.",
            ) from exc

    return result_data


def main() -> None:
    """Main entrypoint for analyze_ci_performance tool."""
    parser = argparse.ArgumentParser(
        description="Analyze GitHub Actions CI workflow performance and generate scorecards."
    )
    parser.add_argument(
        "--workflow",
        default="XMC Unit Tests",
        help="Workflow name filter (default: 'XMC Unit Tests').",
    )
    parser.add_argument(
        "--hours",
        type=float,
        default=None,
        help="Filter runs within the last N hours.",
    )
    parser.add_argument(
        "--since",
        default=None,
        help="ISO timestamp string to filter runs (e.g. '2026-08-20T00:00:00Z').",
    )
    parser.add_argument(
        "--compare-time",
        default=None,
        help="ISO timestamp string to split runs into Before and After periods.",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=100,
        help="Maximum number of runs to fetch (default: 100).",
    )
    parser.add_argument(
        "--markdown",
        action="store_true",
        help="Output formatted Markdown scorecard.",
    )
    parser.add_argument(
        "--output",
        default=None,
        help="Path to write markdown output file.",
    )
    parser.add_argument(
        "--owner",
        default="google",
        help="GitHub repository owner (default: 'google').",
    )
    parser.add_argument(
        "--repo",
        default="xmc",
        help="GitHub repository name (default: 'xmc').",
    )
    run_tool(parser, analyze_ci_performance)


if __name__ == "__main__":
    main()
