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

from tools.common import tool_utils
from tools.common.tool_utils import HandledError
from tools.nightly_tests.triage_nightly_run import create_parser, triage_nightly_run


def _proc(
    stdout: str = "", returncode: int = 0, stderr: str = ""
) -> subprocess.CompletedProcess[str]:
    """Builds a `CompletedProcess` fixture for subprocess mocks."""
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def _json_proc(payload: Any) -> subprocess.CompletedProcess[str]:
    """Builds a successful `CompletedProcess` containing JSON-serialized `payload`."""
    return _proc(json.dumps(payload))


def _run_fixture(
    run_id: int,
    conclusion: str | None,
    sha: str,
    created_at: str,
    status: str = "completed",
    event: str = "schedule",
) -> dict[str, Any]:
    """Builds a baseline GitHub Actions workflow run record."""
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

    def setUp(self) -> None:
        super().setUp()
        self.tmpdir = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.mock_run = self.enterContext(
            patch("tools.nightly_tests.triage_nightly_run.run_command")
        )

    def _triage(self, *argv: str) -> dict[str, Any]:
        return triage_nightly_run(create_parser().parse_args(list(argv)))

    def test_auto_discovers_failure_downloads_selective_artifacts_and_computes_skew(self) -> None:
        scratch = self.tmpdir / "triage_out"
        scratch.mkdir()
        (scratch / "ui-screenshots-symlink").symlink_to(self.tmpdir)

        stale_dir = scratch / "result-resource_dashboard_ui"
        stale_dir.mkdir()
        (stale_dir / "old.json").write_text("{}", encoding="utf-8")

        stale_file = scratch / "ui-screenshots-resource_dashboard_ui"
        stale_file.write_text("stale-file", encoding="utf-8")

        e2e_runs = [
            _run_fixture(9002, None, "9002abcdef12", "2026-11-04T05:00:00Z", status="in_progress"),
            _run_fixture(9001, "failure", "9001aabbccdd", "2026-11-04T04:00:00Z"),
        ]
        jobs_payload = {
            "jobs": [
                {
                    "id": 11,
                    "name": "Run & Verify: Simple Job",
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
            _run_fixture(8003, "success", "8003eeff0011", "2026-11-04T06:00:00Z"),
            _run_fixture(8002, "failure", "8002deadbeef", "2026-11-04T01:00:00Z"),
            _run_fixture(8001, "success", "800111223344", "2026-11-03T01:00:00Z"),
        ]
        diff_files = "\n".join(
            [
                "cmd/rod_data_provider/main.go",
                "deploy/addons/metrics/setup_metrics_addons.sh",
                "deploy/clickhouse/schema.sql",
                "deploy/cli/install_xmanager.py",
                "deploy/local/kind-config.yaml",
                "README.md",
            ]
        )
        self.mock_run.side_effect = [
            _proc(),  # git fetch -- origin main
            _json_proc(e2e_runs),  # gh run list (nightly-examples-e2e.yml)
            _json_proc([{"number": 42, "title": "Failure", "url": "u", "createdAt": "t"}]),
            _json_proc(jobs_payload),  # gh api .../runs/9001/jobs
            _proc("ERROR: Timeout waiting for selector\n"),  # gh api .../jobs/22/logs
            _json_proc(e2e_artifacts),  # gh api .../runs/9001/artifacts
            _proc(),  # gh run download result-resource_dashboard_ui
            _proc(),  # gh run download ui-screenshots-resource_dashboard_ui
            _json_proc(build_runs),  # gh run list (nightly-build.yml)
            _proc("abc1234 feat(rod): add column\ndef5678 docs: readme\n"),  # git log
            _proc(diff_files),  # git diff --name-only
            _json_proc({"artifacts": [{"name": "deploy-gke-diagnostics", "expired": False}]}),
            _proc(),  # gh run download deploy-gke-diagnostics
        ]

        res = self._triage(f"--scratch-dir={scratch}")

        self.assertEqual(res["status_summary"], "failing")
        self.assertEqual(res["target_run"]["databaseId"], 9001)
        self.assertIsNone(res["superseded_by_green_run"])
        self.assertFalse((stale_dir / "old.json").exists())
        self.assertFalse(stale_file.is_file())

        failed_job = res["failed_jobs"][0]
        self.assertEqual(failed_job["job_id"], 22)
        self.assertEqual(failed_job["example_id"], "resource_dashboard_ui")
        self.assertEqual(failed_job["failed_steps"], ["Verify Example Workload"])
        self.assertTrue(
            Path(failed_job["log_path"]).read_text(encoding="utf-8").startswith("ERROR:")
        )

        self.assertEqual(
            [Path(path).name for path in res["downloaded_artifacts"]],
            [
                "result-resource_dashboard_ui",
                "ui-screenshots-resource_dashboard_ui",
                "deploy-gke-diagnostics",
            ],
        )

        skew = res["rollout_skew"]
        self.assertEqual(skew["active_build_run"]["databaseId"], 8002)
        self.assertEqual(skew["last_green_build_run"]["databaseId"], 8001)
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

    def test_workflow_dispatch_green_run_does_not_supersede_scheduled_failure(self) -> None:
        e2e_runs = [
            _run_fixture(
                9050, "success", "9050abcdef12", "2026-11-04T06:00:00Z", event="workflow_dispatch"
            ),
            _run_fixture(9001, "failure", "9001aabbccdd", "2026-11-04T04:00:00Z"),
        ]
        builds = [_run_fixture(8001, "success", "9001aabbccdd", "2026-11-04T01:00:00Z")]
        self.mock_run.side_effect = [
            _proc(),
            _json_proc(e2e_runs),
            _proc("[]"),
            _json_proc({"jobs": []}),
            _json_proc({"artifacts": []}),
            _json_proc(builds),
        ]

        res = self._triage(f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["status_summary"], "failing")
        self.assertEqual(res["target_run"]["databaseId"], 9001)
        self.assertIsNone(res["superseded_by_green_run"])
        self.assertEqual(
            res["warnings"],
            [
                "Newer green workflow_dispatch run 9050 (possibly filtered) "
                "not treated as superseding."
            ],
        )

    def test_auto_discovery_skips_superseded_failures(self) -> None:
        e2e_runs = [
            _run_fixture(9200, "success", "9200aabbccdd", "2026-11-04T04:00:00Z"),
            _run_fixture(
                9175, "success", "9175abcdef12", "2026-11-03T12:00:00Z", event="workflow_dispatch"
            ),
            _run_fixture(9150, "failure", "915011223344", "2026-11-03T04:00:00Z"),
        ]
        self.mock_run.side_effect = [_proc(), _json_proc(e2e_runs), _proc("[]")]

        res = self._triage()

        self.assertEqual(res["status_summary"], "healthy")
        self.assertIsNone(res["target_run"])
        self.assertEqual(res["warnings"], [])
        self.assertEqual(self.mock_run.call_count, 3)

    def test_auto_discovery_reports_no_completed_runs(self) -> None:
        in_progress_runs = [
            _run_fixture(9300, None, "9300abcdef12", "2026-11-04T05:00:00Z", status="in_progress")
        ]
        self.mock_run.side_effect = [_proc(), _json_proc(in_progress_runs), _proc("[]")]

        res = self._triage()

        self.assertEqual(res["status_summary"], "no_completed_runs")
        self.assertTrue(any("No completed" in warning for warning in res["warnings"]))

    def test_explicit_run_id_superseded_and_symlink_log_guard(self) -> None:
        (self.tmpdir / "job_77.log").symlink_to(self.tmpdir / "outside.txt")
        e2e_runs = [_run_fixture(9200, "success", "9200aabbccdd", "2026-11-04T04:00:00Z")]
        explicit_run = _run_fixture(9150, "cancelled", "915011223344", "2026-11-01T04:00:00Z")
        jobs = {
            "jobs": [{"id": 77, "name": "Build Images", "conclusion": "cancelled", "steps": []}]
        }
        newer_builds = [_run_fixture(8500, "failure", "8500deadbeef", "2026-11-04T01:00:00Z")]
        self.mock_run.side_effect = [
            _proc(returncode=1, stderr="offline"),
            _json_proc(e2e_runs),
            _proc("[]"),
            _json_proc(explicit_run),
            _json_proc(jobs),
            _proc("cancelled log"),
            _json_proc({"artifacts": []}),
            _json_proc(newer_builds),
        ]

        res = self._triage("--run-id=9150", f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["status_summary"], "superseded")
        self.assertEqual(res["superseded_by_green_run"]["databaseId"], 9200)
        self.assertIsNone(res["failed_jobs"][0]["log_path"])
        self.assertIsNone(res["rollout_skew"]["active_build_run"])
        for expected in (
            "git fetch",
            "Skipped symlink",
            "15-run lookback window",
            "No prior green",
        ):
            self.assertTrue(any(expected in warning for warning in res["warnings"]), expected)

    def test_explicit_run_id_returns_early_when_in_progress_or_healthy(self) -> None:
        e2e_runs = [_run_fixture(9200, "success", "9200aabbccdd", "2026-11-04T04:00:00Z")]
        cases = (
            (
                _run_fixture(
                    9100, None, "9100abcdef12", "2026-11-01T04:00:00Z", status="in_progress"
                ),
                "in_progress",
            ),
            (e2e_runs[0], "healthy"),
        )
        for pinned_run, expected_status in cases:
            with self.subTest(expected_status=expected_status):
                self.mock_run.reset_mock()
                self.mock_run.side_effect = [
                    _proc(),
                    _json_proc(e2e_runs),
                    _proc("[]"),
                    _json_proc(pinned_run),
                ]

                res = self._triage(f"--run-id={pinned_run['databaseId']}")

                self.assertEqual(res["status_summary"], expected_status)
                self.assertIsNone(res["superseded_by_green_run"])
                self.assertIsNone(res["scratch_dir"])
                self.assertEqual(self.mock_run.call_count, 4)

    def test_rollout_skew_git_inspect_failure_records_warning(self) -> None:
        e2e_runs = [_run_fixture(9001, "failure", "9001aabbccdd", "2026-11-04T04:00:00Z")]
        builds = [_run_fixture(8001, "success", "800111223344", "2026-11-04T01:00:00Z")]
        self.mock_run.side_effect = [
            _proc(),
            _json_proc(e2e_runs),
            _proc("[]"),
            _json_proc({"jobs": []}),
            _json_proc({"artifacts": []}),
            _json_proc(builds),
            _proc(returncode=128, stderr="fatal: bad revision"),
            _proc(""),
        ]

        res = self._triage(f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["rollout_skew"]["undeployed_commits"], [])
        self.assertTrue(any("git inspect" in warning for warning in res["warnings"]))

    def test_invalid_build_sha_warns_and_uses_default_scratch_dir(self) -> None:
        e2e_runs = [_run_fixture(9001, "failure", "9001aabbccdd", "2026-11-04T04:00:00Z")]
        bad_builds = [_run_fixture(8001, "success", "bad..sha", "2026-11-04T01:00:00Z")]
        self.mock_run.side_effect = [
            _proc(),
            _json_proc(e2e_runs),
            _proc("[]"),
            _json_proc({"jobs": []}),
            _json_proc({"artifacts": []}),
            _json_proc(bad_builds),
        ]

        with patch.dict("os.environ", {"ARTIFACT_DIR": ""}), patch.object(Path, "mkdir"):
            res = self._triage()

        self.assertEqual(res["scratch_dir"], "/tmp/nightly_triage_9001")
        self.assertTrue(any("Invalid commit SHA" in warning for warning in res["warnings"]))

    def test_cli_errors_and_missing_binary_handling(self) -> None:
        with self.assertRaises(HandledError):
            self._triage("--run-id=0")

        self.mock_run.side_effect = [_proc(), _proc(returncode=1, stderr="HTTP 401")]
        with self.assertRaisesRegex(HandledError, "HTTP 401"):
            self._triage()

        self.mock_run.side_effect = [_proc(), _proc("")]
        with self.assertRaisesRegex(HandledError, "Invalid JSON"):
            self._triage()

        with (
            patch(
                "tools.nightly_tests.triage_nightly_run.run_command",
                wraps=tool_utils.run_command,
            ),
            patch("tools.common.tool_utils.subprocess.run", side_effect=FileNotFoundError("git")),
            self.assertRaisesRegex(HandledError, "Cannot run git"),
        ):
            self._triage()


if __name__ == "__main__":
    unittest.main()
