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

"""Unit tests for tools.nightly_tests.triage_nightly_run."""

from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.nightly_tests.triage_nightly_run import create_parser, triage_nightly_run


def _proc(
    stdout: str = "", returncode: int = 0, stderr: str = ""
) -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def _run_fixture(
    run_id: int, conclusion: str | None, sha: str, created_at: str, status: str = "completed"
) -> dict[str, Any]:
    return {
        "databaseId": run_id,
        "status": status,
        "conclusion": conclusion,
        "headSha": sha,
        "event": "schedule",
        "createdAt": created_at,
        "url": f"https://github.com/google/xmc/actions/runs/{run_id}",
    }


class TriageNightlyRunTest(unittest.TestCase):
    """Hermetic unit tests for triage_nightly_run."""

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_auto_discovers_failure_downloads_selective_artifacts_and_computes_skew(
        self, mock_run, mock_which
    ) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            scratch = Path(tmpdir) / "triage_out"
            e2e_runs = [
                _run_fixture(
                    9002, None, "sha_in_prog", "2026-11-04T05:00:00Z", status="in_progress"
                ),
                _run_fixture(9001, "failure", "e2e_sha_9001", "2026-11-04T04:00:00Z"),
            ]
            issues = [
                {"number": 42, "title": "Nightly Test Suite Failure", "url": "u", "createdAt": "t"}
            ]
            jobs_payload = {
                "jobs": [
                    {
                        "id": 11,
                        "name": "Run & Verify: Simple Job (3 Multi-Unit Batch)",
                        "conclusion": "success",
                        "steps": [],
                    },
                    {
                        "id": 22,
                        "name": "Run & Verify: Resource Observability Dashboard (Playwright UI)",
                        "conclusion": "failure",
                        "steps": [
                            {"name": "Launch Example Workload", "conclusion": "success"},
                            {"name": "Verify Example Workload", "conclusion": "failure"},
                        ],
                    },
                ]
            }
            e2e_artifacts = {
                "artifacts": [
                    {"name": "result-simple_job", "expired": False},
                    {"name": "result-resource_dashboard_ui", "expired": False},
                    {"name": "ui-screenshots-resource_dashboard_ui", "expired": False},
                    {"name": "ui-screenshots-expired", "expired": True},
                    {"name": "../traversal", "expired": False},
                ]
            }
            build_runs = [
                _run_fixture(8003, "success", "newer_build_sha", "2026-11-04T06:00:00Z"),
                _run_fixture(8002, "failure", "failed_build_sha", "2026-11-04T01:00:00Z"),
                _run_fixture(8001, "success", "green_build_sha", "2026-11-03T01:00:00Z"),
            ]
            build_artifacts = {
                "artifacts": [
                    {"name": "deploy-gke-diagnostics", "expired": False},
                    {"name": "other-build-artifact", "expired": False},
                ]
            }
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc(json.dumps(issues)),
                _proc(json.dumps(jobs_payload)),
                _proc("ERROR: Timeout waiting for selector\n"),
                _proc(json.dumps(e2e_artifacts)),
                _proc(),
                _proc(),
                _proc(json.dumps(build_runs)),
                _proc(json.dumps(build_artifacts)),
                _proc(),
                _proc("abc1234 feat(rod): add column\ndef5678 docs: readme\n"),
                _proc("cmd/rod_data_provider/main.go\nREADME.md\n"),
            ]

            res = triage_nightly_run(create_parser().parse_args([f"--scratch-dir={scratch}"]))
            self.assertEqual(
                (res["status_summary"], res["is_latest_e2e_failing"]), ("failing", True)
            )
            self.assertEqual(res["target_run"]["databaseId"], 9001)
            self.assertIsNone(res["superseded_by_green_run"])
            self.assertEqual(len(res["open_issues"]), 1)
            job = res["failed_jobs"][0]
            self.assertEqual((job["job_id"], job["example_id"]), (22, "resource_dashboard_ui"))
            self.assertEqual(job["failed_steps"], ["Verify Example Workload"])
            self.assertTrue(Path(job["log_path"]).read_text(encoding="utf-8").startswith("ERROR:"))
            self.assertEqual(
                [Path(p).name for p in res["downloaded_artifacts"]],
                [
                    "result-resource_dashboard_ui",
                    "ui-screenshots-resource_dashboard_ui",
                    "deploy-gke-diagnostics",
                ],
            )
            skew = res["rollout_skew"]
            self.assertEqual(
                (
                    skew["active_build_run"]["databaseId"],
                    skew["last_green_build_run"]["databaseId"],
                ),
                (8002, 8001),
            )
            self.assertEqual(len(skew["undeployed_commits"]), 2)
            self.assertEqual(skew["cluster_deployed_files"], ["cmd/rod_data_provider/main.go"])
            self.assertEqual(res["warnings"], [])

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_healthy_main_returns_clean_summary_without_artifact_downloads(
        self, mock_run, mock_which
    ) -> None:
        e2e_runs = [_run_fixture(9100, "success", "green_sha", "2026-11-04T04:00:00Z")]
        mock_run.side_effect = [_proc(), _proc(json.dumps(e2e_runs)), _proc("[]")]
        res = triage_nightly_run(create_parser().parse_args([]))
        self.assertEqual((res["status_summary"], res["is_latest_e2e_failing"]), ("healthy", False))
        self.assertIsNone(res["target_run"])
        self.assertEqual(mock_run.call_count, 3)

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_explicit_run_id_detects_superseding_green_run_and_records_warnings(
        self, mock_run, mock_which
    ) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            e2e_runs = [_run_fixture(9200, "success", "fixed_sha", "2026-11-04T04:00:00Z")]
            explicit_raw = {
                "id": 9150,
                "status": "completed",
                "conclusion": "cancelled",
                "head_sha": "old_sha",
                "event": "schedule",
                "created_at": "2026-11-03T04:00:00Z",
                "html_url": "u_old",
            }
            jobs_payload = {
                "jobs": [
                    {
                        "id": 77,
                        "name": "Build Example Container Images",
                        "conclusion": "cancelled",
                        "steps": [],
                    }
                ]
            }
            mock_run.side_effect = [
                _proc(returncode=1, stderr="offline"),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc(json.dumps(explicit_raw)),
                _proc(json.dumps(jobs_payload)),
                _proc(returncode=1, stderr="HTTP 410 Gone"),
                _proc('{"artifacts": []}'),
                _proc("[]"),
            ]
            res = triage_nightly_run(
                create_parser().parse_args(["--run-id=9150", f"--scratch-dir={tmpdir}"])
            )
            self.assertEqual(
                (res["target_run"]["databaseId"], res["is_latest_e2e_failing"]), (9150, False)
            )
            self.assertEqual(res["superseded_by_green_run"]["databaseId"], 9200)
            self.assertIsNone(res["failed_jobs"][0]["log_path"])
            self.assertEqual(len(res["warnings"]), 2)

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_validation_and_required_gh_errors(self, mock_run, mock_which) -> None:
        mock_which.return_value = None
        with self.assertRaises(HandledError):
            triage_nightly_run(create_parser().parse_args([]))

        mock_which.return_value = "/bin/tool"
        for bad_argv in (
            ["--repo=bad_repo"],
            ["--branch=origin/main"],
            ["--branch=-bad"],
            ["--run-id=0"],
            ["--limit=0"],
        ):
            with self.subTest(bad_argv=bad_argv), self.assertRaises(HandledError):
                triage_nightly_run(create_parser().parse_args(bad_argv))

        mock_run.side_effect = [_proc(), _proc(returncode=1, stderr="HTTP 401")]
        with self.assertRaises(HandledError):
            triage_nightly_run(create_parser().parse_args([]))


if __name__ == "__main__":
    unittest.main()
