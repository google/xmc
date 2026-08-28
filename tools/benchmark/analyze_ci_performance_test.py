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

"""Unit tests for analyze_ci_performance tool."""

import json
import subprocess
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.benchmark.analyze_ci_performance import (
    analyze_ci_performance,
    calculate_comparative_deltas,
    calculate_metrics,
    calculate_percentile,
    calculate_run_duration,
    extract_completed_durations,
    fetch_workflow_runs,
    filter_runs,
    format_duration,
    generate_markdown_scorecard,
    parse_iso_timestamp,
    split_runs_by_compare_time,
)
from tools.common.tool_utils import HandledError


class TestAnalyzeCIPerformance(unittest.TestCase):
    """Test suite for analyze_ci_performance module."""

    def test_parse_iso_timestamp_valid_z(self):
        """Test parsing valid ISO timestamp with trailing Z."""
        dt = parse_iso_timestamp("2026-08-20T12:00:00Z")
        self.assertEqual(dt, datetime(2026, 8, 20, 12, 0, 0, tzinfo=timezone.utc))

    def test_parse_iso_timestamp_valid_offset(self):
        """Test parsing valid ISO timestamp with explicit timezone offset."""
        dt = parse_iso_timestamp("2026-08-20T14:00:00+02:00")
        self.assertEqual(dt, datetime(2026, 8, 20, 12, 0, 0, tzinfo=timezone.utc))

    def test_parse_iso_timestamp_invalid(self):
        """Test parsing malformed ISO timestamp raises HandledError."""
        with self.assertRaises(HandledError):
            parse_iso_timestamp("invalid-date-string")

    def test_calculate_run_duration_valid(self):
        """Test calculating duration between run_started_at and updated_at."""
        run = {
            "run_started_at": "2026-08-20T12:00:00Z",
            "updated_at": "2026-08-20T12:02:30Z",
        }
        duration = calculate_run_duration(run)
        self.assertEqual(duration, 150.0)

    def test_calculate_run_duration_fallback_created_at(self):
        """Test calculating duration using created_at fallback."""
        run = {
            "created_at": "2026-08-20T12:00:00Z",
            "updated_at": "2026-08-20T12:01:00Z",
        }
        duration = calculate_run_duration(run)
        self.assertEqual(duration, 60.0)

    def test_calculate_run_duration_missing_or_invalid(self):
        """Test duration returns None when timestamps are missing or invalid."""
        self.assertIsNone(calculate_run_duration({}))
        self.assertIsNone(calculate_run_duration({"run_started_at": "2026-08-20T12:00:00Z"}))
        self.assertIsNone(
            calculate_run_duration(
                {
                    "run_started_at": "2026-08-20T12:05:00Z",
                    "updated_at": "2026-08-20T12:00:00Z",
                }
            )
        )

    def test_calculate_percentile_empty(self):
        """Test calculate_percentile returns 0.0 on empty list."""
        self.assertEqual(calculate_percentile([], 0.90), 0.0)

    def test_calculate_percentile_single_value(self):
        """Test calculate_percentile on single value."""
        self.assertEqual(calculate_percentile([42.0], 0.90), 42.0)

    def test_calculate_percentile_multiple_values(self):
        """Test linear interpolation for percentile calculation."""
        data = [10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0]
        # rank = 9 * 0.90 = 8.1 -> 90.0 * 0.9 + 100.0 * 0.1 = 91.0
        self.assertAlmostEqual(calculate_percentile(data, 0.90), 91.0)
        self.assertAlmostEqual(calculate_percentile(data, 0.50), 55.0)

    def test_calculate_metrics_empty(self):
        """Test calculate_metrics with empty list."""
        metrics = calculate_metrics([])
        self.assertEqual(
            metrics,
            {
                "count": 0,
                "mean": 0.0,
                "median": 0.0,
                "p90": 0.0,
                "min": 0.0,
                "max": 0.0,
            },
        )

    def test_calculate_metrics_values(self):
        """Test calculate_metrics with synthetic values."""
        durations = [100.0, 200.0, 300.0, 400.0, 500.0]
        metrics = calculate_metrics(durations)
        self.assertEqual(metrics["count"], 5)
        self.assertEqual(metrics["mean"], 300.0)
        self.assertEqual(metrics["median"], 300.0)
        self.assertEqual(metrics["min"], 100.0)
        self.assertEqual(metrics["max"], 500.0)
        self.assertAlmostEqual(metrics["p90"], 460.0)

    def test_filter_runs(self):
        """Test filtering runs by workflow name and since timestamp."""
        runs = [
            {
                "name": "XMC Unit Tests",
                "created_at": "2026-08-20T10:00:00Z",
                "status": "completed",
            },
            {
                "name": "XMC Unit Tests",
                "created_at": "2026-08-20T08:00:00Z",
                "status": "completed",
            },
            {
                "name": "Other Workflow",
                "created_at": "2026-08-20T10:00:00Z",
                "status": "completed",
            },
        ]
        since_dt = datetime(2026, 8, 20, 9, 0, 0, tzinfo=timezone.utc)
        filtered = filter_runs(runs, workflow_name="XMC Unit Tests", since_dt=since_dt)
        self.assertEqual(len(filtered), 1)
        self.assertEqual(filtered[0]["created_at"], "2026-08-20T10:00:00Z")

    def test_split_runs_by_compare_time(self):
        """Test splitting runs into Before and After periods."""
        runs = [
            {"created_at": "2026-08-20T08:00:00Z"},
            {"created_at": "2026-08-20T09:30:00Z"},
            {"created_at": "2026-08-20T11:00:00Z"},
        ]
        cutoff = datetime(2026, 8, 20, 9, 0, 0, tzinfo=timezone.utc)
        before, after = split_runs_by_compare_time(runs, cutoff)
        self.assertEqual(len(before), 1)
        self.assertEqual(before[0]["created_at"], "2026-08-20T08:00:00Z")
        self.assertEqual(len(after), 2)
        self.assertEqual(after[0]["created_at"], "2026-08-20T09:30:00Z")

    def test_extract_completed_durations(self):
        """Test extracting durations for completed runs only."""
        runs = [
            {
                "status": "completed",
                "run_started_at": "2026-08-20T10:00:00Z",
                "updated_at": "2026-08-20T10:02:00Z",
            },
            {
                "status": "in_progress",
                "run_started_at": "2026-08-20T10:00:00Z",
                "updated_at": "2026-08-20T10:02:00Z",
            },
        ]
        durations = extract_completed_durations(runs)
        self.assertEqual(durations, [120.0])

    def test_calculate_comparative_deltas(self):
        """Test computing statistical deltas and percentage changes."""
        before = {
            "count": 10,
            "mean": 100.0,
            "median": 100.0,
            "p90": 150.0,
            "min": 50.0,
            "max": 200.0,
        }
        after = {
            "count": 15,
            "mean": 80.0,
            "median": 75.0,
            "p90": 120.0,
            "min": 40.0,
            "max": 160.0,
        }
        deltas = calculate_comparative_deltas(before, after)
        self.assertEqual(deltas["count_delta"], 5)
        self.assertEqual(deltas["mean_delta_sec"], -20.0)
        self.assertEqual(deltas["mean_pct_delta"], -20.0)
        self.assertEqual(deltas["median_delta_sec"], -25.0)
        self.assertEqual(deltas["median_pct_delta"], -25.0)

    def test_format_duration(self):
        """Test formatting duration strings."""
        self.assertEqual(format_duration(45.2), "45.20s")
        self.assertEqual(format_duration(125.5), "2m 05.50s (125.50s)")
        self.assertEqual(format_duration(-30.0), "-30.00s")

    def test_generate_markdown_scorecard_single(self):
        """Test generating single period markdown scorecard."""
        summary = {
            "workflow": "XMC Unit Tests",
            "owner": "google",
            "repo": "xmc",
            "is_comparison": False,
            "metrics": {
                "count": 5,
                "median": 60.0,
                "mean": 65.0,
                "p90": 80.0,
                "min": 50.0,
                "max": 90.0,
            },
        }
        md = generate_markdown_scorecard(summary)
        self.assertIn("### ⏱️ CI Performance Scorecard", md)
        self.assertIn("XMC Unit Tests", md)
        self.assertIn("Median Duration", md)

    def test_generate_markdown_scorecard_comparison(self):
        """Test generating comparison markdown scorecard with speedup banner."""
        summary = {
            "workflow": "XMC Unit Tests",
            "owner": "google",
            "repo": "xmc",
            "is_comparison": True,
            "compare_time": "2026-08-20T12:00:00Z",
            "before": {
                "metrics": {
                    "count": 10,
                    "median": 100.0,
                    "mean": 100.0,
                    "p90": 150.0,
                    "min": 50.0,
                    "max": 200.0,
                }
            },
            "after": {
                "metrics": {
                    "count": 10,
                    "median": 80.0,
                    "mean": 80.0,
                    "p90": 120.0,
                    "min": 40.0,
                    "max": 160.0,
                }
            },
            "deltas": {
                "count_delta": 0,
                "median_delta_sec": -20.0,
                "median_pct_delta": -20.0,
                "mean_delta_sec": -20.0,
                "mean_pct_delta": -20.0,
                "p90_delta_sec": -30.0,
                "p90_pct_delta": -20.0,
                "min_delta_sec": -10.0,
                "min_pct_delta": -20.0,
                "max_delta_sec": -40.0,
                "max_pct_delta": -20.0,
            },
        }
        md = generate_markdown_scorecard(summary)
        self.assertIn("🟢 **Speedup**", md)
        self.assertIn("| **Median Duration** |", md)

    @patch("subprocess.run")
    def test_fetch_workflow_runs_pagination(self, mock_run):
        """Test fetching workflow runs with pagination handling."""
        page1 = {"workflow_runs": [{"id": i} for i in range(100)]}
        page2 = {"workflow_runs": [{"id": 100}]}
        mock_run.side_effect = [
            MagicMock(stdout=json.dumps(page1)),
            MagicMock(stdout=json.dumps(page2)),
        ]
        runs = fetch_workflow_runs(owner="google", repo="xmc", limit=101)
        self.assertEqual(len(runs), 101)

    @patch("subprocess.run")
    def test_fetch_workflow_runs_error(self, mock_run):
        """Test fetch_workflow_runs raises HandledError on subprocess error."""
        mock_run.side_effect = subprocess.CalledProcessError(1, ["gh"], stderr="API error")
        with self.assertRaises(HandledError):
            fetch_workflow_runs()

    @patch("tools.benchmark.analyze_ci_performance.fetch_workflow_runs")
    def test_analyze_ci_performance_orchestration(self, mock_fetch):
        """Test analyze_ci_performance orchestration and file writing."""
        mock_fetch.return_value = [
            {
                "name": "XMC Unit Tests",
                "status": "completed",
                "run_started_at": "2026-08-20T10:00:00Z",
                "updated_at": "2026-08-20T10:01:00Z",
            },
            {
                "name": "XMC Unit Tests",
                "status": "completed",
                "run_started_at": "2026-08-20T14:00:00Z",
                "updated_at": "2026-08-20T14:01:30Z",
            },
        ]
        with tempfile.TemporaryDirectory() as tmp_dir:
            out_file = Path(tmp_dir) / "scorecard.md"
            args = MagicMock(
                workflow="XMC Unit Tests",
                owner="google",
                repo="xmc",
                hours=None,
                since=None,
                compare_time="2026-08-20T12:00:00Z",
                limit=10,
                markdown=True,
                output=str(out_file),
            )
            res = analyze_ci_performance(args)
            self.assertTrue(res["is_comparison"])
            self.assertEqual(res["before"]["completed_runs"], 1)
            self.assertEqual(res["after"]["completed_runs"], 1)
            self.assertTrue(out_file.is_file())


if __name__ == "__main__":
    unittest.main()
