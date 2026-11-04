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
    run_id: int,
    conclusion: str | None,
    sha: str,
    created_at: str,
    status: str = "completed",
    event: str = "schedule",
) -> dict[str, Any]:
    return {
        "databaseId": run_id,
        "status": status,
        "conclusion": conclusion,
        "headSha": sha,
        "event": event,
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
            scratch.mkdir(parents=True, exist_ok=True)
            (scratch / "ui-screenshots-symlink").symlink_to(tmpdir)
            stale_dir = scratch / "result-resource_dashboard_ui"
            stale_dir.mkdir()
            (stale_dir / "old.json").write_text("{}", encoding="utf-8")

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
                    {"name": "ui-screenshots-../traversal", "expired": False},
                    {"name": "ui-screenshots-symlink", "expired": False},
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
            diff_out = "\n".join(
                [
                    "cmd/rod_data_provider/main.go",
                    "deploy/addons/metrics/setup_metrics_addons.sh",
                    "deploy/clickhouse/schema.sql",
                    "deploy/cli/install_xmanager.py",
                    "deploy/local/kind-config.yaml",
                    "README.md",
                ]
            )
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
                _proc(diff_out),
            ]

            res = triage_nightly_run(create_parser().parse_args([f"--scratch-dir={scratch}"]))
            self.assertEqual(
                (res["status_summary"], res["is_latest_e2e_failing"]), ("failing", True)
            )
            self.assertEqual(res["target_run"]["databaseId"], 9001)
            self.assertIsNone(res["superseded_by_green_run"])
            self.assertFalse((stale_dir / "old.json").exists())
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
            self.assertEqual(
                skew["cluster_deployed_files"],
                [
                    "cmd/rod_data_provider/main.go",
                    "deploy/addons/metrics/setup_metrics_addons.sh",
                    "deploy/clickhouse/schema.sql",
                ],
            )
            self.assertEqual(res["warnings"], [])

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_workflow_dispatch_green_run_does_not_supersede_scheduled_failure(
        self, mock_run, mock_which
    ) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            e2e_runs = [
                _run_fixture(
                    9050,
                    "success",
                    "dispatch_sha",
                    "2026-11-04T06:00:00Z",
                    event="workflow_dispatch",
                ),
                _run_fixture(9001, "failure", "sched_fail_sha", "2026-11-04T04:00:00Z"),
                _run_fixture(8990, "success", "prev_sched_sha", "2026-11-03T04:00:00Z"),
            ]
            builds = [_run_fixture(8001, "success", "sched_fail_sha", "2026-11-04T01:00:00Z")]
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc('{"jobs": []}'),
                _proc('{"artifacts": []}'),
                _proc(json.dumps(builds)),
            ]
            res = triage_nightly_run(create_parser().parse_args([f"--scratch-dir={tmpdir}"]))
            self.assertEqual(
                (res["status_summary"], res["is_latest_e2e_failing"]), ("failing", True)
            )
            self.assertEqual(res["target_run"]["databaseId"], 9001)
            self.assertEqual(res["latest_completed_e2e_run"]["databaseId"], 9050)
            self.assertIsNone(res["superseded_by_green_run"])
            self.assertEqual(
                res["warnings"],
                [
                    "Newer green workflow_dispatch run 9050 (possibly filtered) "
                    "not treated as superseding."
                ],
            )

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_auto_discovery_skips_superseded_historical_failures_and_handles_empty_runs(
        self, mock_run, mock_which
    ) -> None:
        e2e_runs = [
            _run_fixture(9200, "success", "green_sha", "2026-11-04T04:00:00Z"),
            _run_fixture(
                9175, "success", "wd_sha", "2026-11-03T12:00:00Z", event="workflow_dispatch"
            ),
            _run_fixture(9150, "failure", "old_fail_sha", "2026-11-03T04:00:00Z"),
        ]
        mock_run.side_effect = [_proc(), _proc(json.dumps(e2e_runs)), _proc("[]")]
        res = triage_nightly_run(create_parser().parse_args([]))
        self.assertEqual((res["status_summary"], res["is_latest_e2e_failing"]), ("healthy", False))
        self.assertIsNone(res["target_run"])
        self.assertEqual(res["warnings"], [])
        self.assertEqual(mock_run.call_count, 3)

        in_prog = [_run_fixture(9300, None, "sha", "2026-11-04T05:00:00Z", status="in_progress")]
        mock_run.side_effect = [_proc(), _proc(json.dumps(in_prog)), _proc("[]")]
        empty_res = triage_nightly_run(create_parser().parse_args([]))
        self.assertEqual(empty_res["status_summary"], "no_completed_runs")
        self.assertEqual(len(empty_res["warnings"]), 1)

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_explicit_run_id_reports_superseded_status_and_post_target_build_warnings(
        self, mock_run, mock_which
    ) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            e2e_runs = [
                _run_fixture(9200, "success", "fixed_sha", "2026-11-04T04:00:00Z"),
                _run_fixture(
                    9175, "success", "wd_sha", "2026-11-02T06:00:00Z", event="workflow_dispatch"
                ),
            ]
            explicit_run = _run_fixture(9150, "cancelled", "old_sha", "2026-11-01T04:00:00Z")
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
            newer_builds = [
                _run_fixture(8500, "failure", "post_target_sha", "2026-11-04T01:00:00Z")
            ]
            mock_run.side_effect = [
                _proc(returncode=1, stderr="offline"),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc(json.dumps(explicit_run)),
                _proc(json.dumps(jobs_payload)),
                _proc(returncode=1, stderr="HTTP 410 Gone"),
                _proc('{"artifacts": []}'),
                _proc(json.dumps(newer_builds)),
            ]
            res = triage_nightly_run(
                create_parser().parse_args(["--run-id=9150", f"--scratch-dir={tmpdir}"])
            )
            self.assertEqual(
                (res["status_summary"], res["is_latest_e2e_failing"]), ("superseded", False)
            )
            self.assertEqual(res["superseded_by_green_run"]["databaseId"], 9200)
            self.assertIsNone(res["rollout_skew"]["active_build_run"])
            self.assertEqual(res["downloaded_artifacts"], [])
            self.assertEqual(len(res["warnings"]), 4)
            self.assertTrue(any("15-run lookback window" in w for w in res["warnings"]))

            # Explicit in_progress and healthy runs return early without downloading or setting sup
            in_prog_run = _run_fixture(
                9100, None, "sha", "2026-11-01T04:00:00Z", status="in_progress"
            )
            mock_run.reset_mock()
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc(json.dumps(in_prog_run)),
            ]
            ip_res = triage_nightly_run(create_parser().parse_args(["--run-id=9100"]))
            self.assertEqual(ip_res["status_summary"], "in_progress")
            self.assertIsNone(ip_res["superseded_by_green_run"])
            self.assertIsNone(ip_res["scratch_dir"])
            self.assertEqual(mock_run.call_count, 4)

            mock_run.reset_mock()
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc(json.dumps(e2e_runs[0])),
            ]
            ok_res = triage_nightly_run(create_parser().parse_args(["--run-id=9200"]))
            self.assertEqual(ok_res["status_summary"], "healthy")
            self.assertIsNone(ok_res["rollout_skew"])
            self.assertEqual(mock_run.call_count, 4)

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which", return_value="/bin/tool")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_rollout_skew_records_warning_when_git_log_fails(self, mock_run, mock_which) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            e2e_runs = [_run_fixture(9001, "failure", "e2e_sha", "2026-11-04T04:00:00Z")]
            builds = [_run_fixture(8001, "success", "green_sha", "2026-11-04T01:00:00Z")]
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc('{"jobs": []}'),
                _proc('{"artifacts": []}'),
                _proc(json.dumps(builds)),
                _proc(returncode=128, stderr="fatal: bad revision"),
                _proc(""),
            ]
            res = triage_nightly_run(create_parser().parse_args([f"--scratch-dir={tmpdir}"]))
            self.assertEqual(res["rollout_skew"]["undeployed_commits"], [])
            self.assertTrue(any("git inspect" in w for w in res["warnings"]))

            bad_builds = [_run_fixture(8001, "success", "bad..sha", "2026-11-04T01:00:00Z")]
            mock_run.side_effect = [
                _proc(),
                _proc(json.dumps(e2e_runs)),
                _proc("[]"),
                _proc('{"jobs": []}'),
                _proc('{"artifacts": []}'),
                _proc(json.dumps(bad_builds)),
            ]
            with patch.dict("os.environ", {"ARTIFACT_DIR": ""}), patch.object(Path, "mkdir"):
                bad_res = triage_nightly_run(create_parser().parse_args([]))
            self.assertEqual(bad_res["scratch_dir"], "/tmp/nightly_triage_9001")
            self.assertTrue(any("Invalid commit SHA" in w for w in bad_res["warnings"]))

    @patch("tools.nightly_tests.triage_nightly_run.shutil.which")
    @patch("tools.nightly_tests.triage_nightly_run.run_command")
    def test_validation_and_required_gh_errors(self, mock_run, mock_which) -> None:
        mock_which.return_value = None
        with self.assertRaises(HandledError):
            triage_nightly_run(create_parser().parse_args([]))

        mock_which.return_value = "/bin/tool"
        for bad_argv in (
            ["--repo=bad_repo"],
            ["--repo=../evil"],
            ["--repo=-flag/xmc"],
            ["--branch=origin/main"],
            ["--branch=-bad"],
            ["--branch=main..HEAD"],
            ["--branch=main:other"],
            ["--branch=feat/trailing/"],
            ["--branch=feat//double"],
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
