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

"""Unit tests for tools.release.dispatch_and_watch."""

import json
import subprocess
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.release.dispatch_and_watch import (
    EXIT_CANCELLED,
    EXIT_FAILURE,
    EXIT_SHA_MISMATCH,
    EXIT_SUCCESS,
    RunOutcome,
    WorkflowRun,
    check_superseded_by,
    create_parser,
    dispatch_and_watch,
    dispatch_workflow,
    extract_page_url,
    find_dispatched_run,
    find_existing_run,
    main,
    parse_iso8601_utc,
    resolve_actor,
    update_state_file,
    watch_run,
)


class DispatchAndWatchTest(unittest.TestCase):
    """Test suite for dispatch_and_watch CLI tool and helpers."""

    def test_parse_iso8601_utc(self):
        """Validates ISO 8601 string parsing into timezone-aware UTC datetime."""
        dt1 = parse_iso8601_utc("2026-09-25T12:00:00Z")
        self.assertEqual(dt1.tzinfo, timezone.utc)
        self.assertEqual(dt1.year, 2026)
        self.assertEqual(dt1.hour, 12)

        dt2 = parse_iso8601_utc("2026-09-25T14:30:00+00:00")
        self.assertEqual(dt2, datetime(2026, 9, 25, 14, 30, 0, tzinfo=timezone.utc))

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value=None)
    def test_run_gh_cmd_gh_missing(self, mock_which):
        """Raises HandledError if gh is not installed in system PATH."""
        with self.assertRaises(HandledError) as ctx:
            dispatch_workflow("nightly-build.yml")
        self.assertIn("not installed", ctx.exception.message)
        self.assertEqual(ctx.exception.exit_code, EXIT_FAILURE)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_dispatch_workflow_success(self, mock_run, mock_which):
        """Dispatches workflow using gh CLI."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "workflow", "run"], returncode=0, stdout="", stderr=""
        )

        t0 = dispatch_workflow("nightly-build.yml", ref="main")
        self.assertIsInstance(t0, datetime)
        mock_run.assert_called_once_with(
            ["gh", "workflow", "run", "nightly-build.yml", "--ref", "main"],
            capture_output=True,
            text=True,
            check=False,
            cwd=None,
        )

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_dispatch_workflow_failure(self, mock_run, mock_which):
        """Raises HandledError if gh workflow run exits non-zero."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "workflow", "run"],
            returncode=1,
            stdout="",
            stderr="could not find workflow",
        )

        with self.assertRaises(HandledError) as ctx:
            dispatch_workflow("unknown.yml", ref="main")
        self.assertIn("could not find workflow", ctx.exception.message)
        self.assertEqual(ctx.exception.exit_code, EXIT_FAILURE)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_existing_run_found(self, mock_run, mock_which):
        """Returns existing completed successful run matching target SHA."""
        runs_json = json.dumps(
            [
                {
                    "databaseId": 12345,
                    "headSha": "0123456789abcdef",
                    "event": "workflow_dispatch",
                    "status": "completed",
                    "conclusion": "success",
                    "createdAt": "2026-09-25T10:00:00Z",
                    "url": "https://github.com/google/xmc/actions/runs/12345",
                },
                {
                    "databaseId": 12340,
                    "headSha": "oldsha",
                    "event": "schedule",
                    "status": "completed",
                    "conclusion": "success",
                    "createdAt": "2026-09-24T10:00:00Z",
                    "url": "https://github.com/google/xmc/actions/runs/12340",
                },
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        run = find_existing_run("nightly-build.yml", "0123456789abcdef", ref="main")
        self.assertIsNotNone(run)
        assert run is not None
        self.assertEqual(run.run_id, 12345)
        self.assertEqual(run.head_sha, "0123456789abcdef")

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_existing_run_not_found(self, mock_run, mock_which):
        """Returns None if no matching successful run exists for target SHA."""
        runs_json = json.dumps(
            [
                {
                    "databaseId": 12345,
                    "headSha": "0123456789abcdef",
                    "event": "workflow_dispatch",
                    "status": "completed",
                    "conclusion": "failure",
                    "createdAt": "2026-09-25T10:00:00Z",
                    "url": "https://github.com/google/xmc/actions/runs/12345",
                }
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        run = find_existing_run("nightly-build.yml", "0123456789abcdef", ref="main")
        self.assertIsNone(run)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_dispatched_run_success(self, mock_run, mock_which):
        """Discovers newly registered run matching timestamp and validates SHA."""
        since = datetime(2026, 9, 25, 11, 0, 0, tzinfo=timezone.utc)
        runs_json = json.dumps(
            [
                {
                    "databaseId": 99999,
                    "headSha": "target_sha_123",
                    "event": "workflow_dispatch",
                    "status": "queued",
                    "conclusion": "",
                    "createdAt": "2026-09-25T11:00:02Z",
                    "url": "https://github.com/google/xmc/actions/runs/99999",
                }
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        run = find_dispatched_run(
            "nightly-build.yml",
            target_sha="target_sha_123",
            since=since,
            actor="testuser",
            ref="main",
            max_attempts=1,
        )
        self.assertEqual(run.run_id, 99999)
        self.assertEqual(run.head_sha, "target_sha_123")
        self.assertIn("--user=testuser", mock_run.call_args[0][0])
        self.assertIn("--event=workflow_dispatch", mock_run.call_args[0][0])

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_dispatched_run_multiple_runs_picks_matching_sha(self, mock_run, mock_which):
        """When multiple runs are registered, picks the one matching target SHA."""
        since = datetime(2026, 9, 25, 11, 0, 0, tzinfo=timezone.utc)
        runs_json = json.dumps(
            [
                {
                    "databaseId": 88888,
                    "headSha": "concurrent_different_sha",
                    "event": "workflow_dispatch",
                    "status": "queued",
                    "conclusion": "",
                    "createdAt": "2026-09-25T11:00:03Z",
                    "url": "https://github.com/google/xmc/actions/runs/88888",
                },
                {
                    "databaseId": 77777,
                    "headSha": "target_sha_123",
                    "event": "workflow_dispatch",
                    "status": "queued",
                    "conclusion": "",
                    "createdAt": "2026-09-25T11:00:01Z",
                    "url": "https://github.com/google/xmc/actions/runs/77777",
                },
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        run = find_dispatched_run(
            "nightly-build.yml",
            target_sha="target_sha_123",
            since=since,
            ref="main",
            max_attempts=1,
        )
        self.assertEqual(run.run_id, 77777)
        self.assertEqual(run.head_sha, "target_sha_123")

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_dispatched_run_sha_mismatch(self, mock_run, mock_which):
        """Raises HandledError with EXIT_SHA_MISMATCH (3) if run has unexpected commit SHA."""
        since = datetime(2026, 9, 25, 11, 0, 0, tzinfo=timezone.utc)
        runs_json = json.dumps(
            [
                {
                    "databaseId": 88888,
                    "headSha": "different_sha_456",
                    "event": "workflow_dispatch",
                    "status": "queued",
                    "conclusion": "",
                    "createdAt": "2026-09-25T11:00:02Z",
                    "url": "https://github.com/google/xmc/actions/runs/88888",
                }
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        with self.assertRaises(HandledError) as ctx:
            find_dispatched_run(
                "nightly-build.yml",
                target_sha="target_sha_123",
                since=since,
                ref="main",
                max_attempts=1,
            )
        self.assertEqual(ctx.exception.exit_code, EXIT_SHA_MISMATCH)
        self.assertIn("Target SHA mismatch", ctx.exception.message)
        self.assertEqual(ctx.exception.details["expected_sha"], "target_sha_123")
        self.assertEqual(ctx.exception.details["actual_sha"], "different_sha_456")

    @patch("tools.release.dispatch_and_watch.time.sleep")
    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_dispatched_run_timeout(self, mock_run, mock_which, mock_sleep):
        """Raises HandledError if polling expires without discovering dispatched run."""
        since = datetime(2026, 9, 25, 11, 0, 0, tzinfo=timezone.utc)
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout="[]", stderr=""
        )

        with self.assertRaises(HandledError) as ctx:
            find_dispatched_run(
                "nightly-build.yml",
                target_sha="target_sha_123",
                since=since,
                ref="main",
                max_attempts=2,
                poll_interval_s=0.01,
            )
        self.assertEqual(ctx.exception.exit_code, EXIT_FAILURE)
        self.assertIn("Timed out waiting", ctx.exception.message)

    @patch(
        "tools.release.dispatch_and_watch.resolve_repo_owner_and_name",
        return_value=("google", "xmc"),
    )
    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_extract_page_url(self, mock_run, mock_which, mock_owner):
        """Extracts Pages URL from gh api or fallback."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "api"],
            returncode=0,
            stdout="https://laughing-adventure.pages.github.io/\n",
            stderr="",
        )
        jobs_data = [{"name": "Deploy Gallery to GitHub Pages", "steps": []}]
        url = extract_page_url("nightly-visual-snapshot.yml", jobs_data)
        self.assertEqual(url, "https://laughing-adventure.pages.github.io/")

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_check_superseded_by(self, mock_run, mock_which):
        """Discovers newer run ID when a cancelled run was superseded."""
        runs_json = json.dumps(
            [
                {"databaseId": 55555, "createdAt": "2026-09-25T12:00:10Z"},
                {"databaseId": 44444, "createdAt": "2026-09-25T12:00:00Z"},
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "list"], returncode=0, stdout=runs_json, stderr=""
        )

        superseded = check_superseded_by(
            44444,
            "nightly-build.yml",
            "main",
            run_created_at=datetime(2026, 9, 25, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertEqual(superseded, 55555)

    @patch("tools.release.dispatch_and_watch.time.sleep")
    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_watch_run_success(self, mock_run, mock_which, mock_sleep):
        """Monitors run to completed success outcome."""
        run_view_json = json.dumps(
            {
                "databaseId": 12345,
                "headSha": "target_sha",
                "event": "workflow_dispatch",
                "status": "completed",
                "conclusion": "success",
                "url": "https://github.com/google/xmc/actions/runs/12345",
                "jobs": [
                    {
                        "name": "Build",
                        "status": "completed",
                        "conclusion": "success",
                        "steps": [],
                        "url": "https://github.com/google/xmc/actions/runs/12345/job/1",
                    }
                ],
            }
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "view"], returncode=0, stdout=run_view_json, stderr=""
        )

        outcome = watch_run(12345, "nightly-build.yml", ref="main", poll_interval_s=0.01)
        self.assertEqual(outcome.run_id, 12345)
        self.assertEqual(outcome.conclusion, "success")
        self.assertEqual(outcome.failed_jobs, [])

    @patch("tools.release.dispatch_and_watch.time.sleep")
    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_watch_run_failure(self, mock_run, mock_which, mock_sleep):
        """Monitors run to failure and extracts failed jobs and steps."""
        run_view_json = json.dumps(
            {
                "databaseId": 12345,
                "headSha": "target_sha",
                "event": "workflow_dispatch",
                "status": "completed",
                "conclusion": "failure",
                "url": "https://github.com/google/xmc/actions/runs/12345",
                "jobs": [
                    {
                        "name": "E2E Integration Tests",
                        "status": "completed",
                        "conclusion": "failure",
                        "url": "https://github.com/google/xmc/actions/runs/12345/job/2",
                        "steps": [
                            {"name": "Checkout", "conclusion": "success"},
                            {"name": "Run Matrix", "conclusion": "failure"},
                        ],
                    }
                ],
            }
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "run", "view"], returncode=0, stdout=run_view_json, stderr=""
        )

        outcome = watch_run(12345, "nightly-examples-e2e.yml", ref="main", poll_interval_s=0.01)
        self.assertEqual(outcome.conclusion, "failure")
        self.assertEqual(len(outcome.failed_jobs), 1)
        self.assertEqual(outcome.failed_jobs[0]["name"], "E2E Integration Tests")
        self.assertEqual(outcome.failed_jobs[0]["failed_steps"], ["Run Matrix"])

    def test_update_state_file(self):
        """Verifies state persistence to JSON file."""
        with tempfile.TemporaryDirectory() as tmpdir:
            state_file = Path(tmpdir) / "release_state.json"
            update_state_file(
                state_file,
                target_sha="sha_1",
                workflow="nightly-build.yml",
                run_info={"run_id": 100, "conclusion": "success"},
            )

            self.assertTrue(state_file.exists())
            with open(state_file, encoding="utf-8") as f:
                data = json.load(f)

            self.assertEqual(data["target_sha"], "sha_1")
            self.assertEqual(data["runs"]["nightly-build.yml"]["run_id"], 100)
            self.assertEqual(data["last_run"]["workflow"], "nightly-build.yml")

    @patch("tools.release.dispatch_and_watch.resolve_target_commit", return_value="resolved_sha")
    @patch(
        "tools.release.dispatch_and_watch.dispatch_workflow",
        return_value=datetime.now(timezone.utc),
    )
    @patch("tools.release.dispatch_and_watch.find_dispatched_run")
    @patch("tools.release.dispatch_and_watch.watch_run")
    def test_dispatch_and_watch_happy_path(
        self, mock_watch, mock_find, mock_dispatch, mock_resolve
    ):
        """Full end-to-end happy path dispatch, find, and watch."""
        mock_find.return_value = WorkflowRun(
            run_id=777,
            url="https://github.com/google/xmc/actions/runs/777",
            head_sha="resolved_sha",
            event="workflow_dispatch",
            status="queued",
            created_at=datetime.now(timezone.utc),
        )
        mock_watch.return_value = RunOutcome(
            run_id=777,
            url="https://github.com/google/xmc/actions/runs/777",
            head_sha="resolved_sha",
            event="workflow_dispatch",
            conclusion="success",
            superseded_by=None,
            failed_jobs=[],
            page_url=None,
        )

        with tempfile.TemporaryDirectory() as tmpdir:
            state_file = Path(tmpdir) / "state.json"
            parser = create_parser()
            args = parser.parse_args(
                [
                    "--workflow",
                    "nightly-build.yml",
                    "--state-file",
                    str(state_file),
                ]
            )
            result = dispatch_and_watch(args)

            self.assertEqual(result["run_id"], 777)
            self.assertEqual(result["conclusion"], "success")
            self.assertEqual(result["_exit_code"], EXIT_SUCCESS)

            # Check state file was written
            with open(state_file, encoding="utf-8") as f:
                state_data = json.load(f)
            self.assertEqual(state_data["target_sha"], "resolved_sha")
            self.assertEqual(state_data["runs"]["nightly-build.yml"]["conclusion"], "success")

    @patch("tools.release.dispatch_and_watch.find_existing_run")
    @patch("tools.release.dispatch_and_watch.watch_run")
    def test_dispatch_and_watch_find_existing(self, mock_watch, mock_find_existing):
        """Reuses existing successful run without dispatching new run."""
        mock_find_existing.return_value = WorkflowRun(
            run_id=555,
            url="https://github.com/google/xmc/actions/runs/555",
            head_sha="existing_sha",
            event="workflow_dispatch",
            status="completed",
            created_at=datetime.now(timezone.utc),
        )
        mock_watch.return_value = RunOutcome(
            run_id=555,
            url="https://github.com/google/xmc/actions/runs/555",
            head_sha="existing_sha",
            event="workflow_dispatch",
            conclusion="success",
            superseded_by=None,
            failed_jobs=[],
            page_url=None,
        )

        parser = create_parser()
        args = parser.parse_args(
            [
                "--workflow",
                "nightly-build.yml",
                "--target-sha",
                "existing_sha",
                "--find-existing",
            ]
        )
        result = dispatch_and_watch(args)

        self.assertEqual(result["run_id"], 555)
        self.assertEqual(result["conclusion"], "success")
        self.assertEqual(result["_exit_code"], EXIT_SUCCESS)

    @patch(
        "tools.release.dispatch_and_watch.dispatch_workflow",
        return_value=datetime.now(timezone.utc),
    )
    @patch("tools.release.dispatch_and_watch.find_dispatched_run")
    def test_dispatch_and_watch_no_watch(self, mock_find, mock_dispatch):
        """Returns in-progress result immediately when --no-watch is specified."""
        mock_find.return_value = WorkflowRun(
            run_id=333,
            url="https://github.com/google/xmc/actions/runs/333",
            head_sha="sha_target",
            event="workflow_dispatch",
            status="queued",
            created_at=datetime.now(timezone.utc),
        )

        parser = create_parser()
        args = parser.parse_args(
            [
                "--workflow",
                "nightly-build.yml",
                "--target-sha",
                "sha_target",
                "--no-watch",
            ]
        )
        result = dispatch_and_watch(args)

        self.assertEqual(result["run_id"], 333)
        self.assertEqual(result["conclusion"], "in_progress")
        self.assertEqual(result["_exit_code"], EXIT_SUCCESS)

    @patch(
        "tools.release.dispatch_and_watch.dispatch_workflow",
        return_value=datetime.now(timezone.utc),
    )
    @patch("tools.release.dispatch_and_watch.find_dispatched_run")
    @patch("tools.release.dispatch_and_watch.watch_run")
    def test_dispatch_and_watch_cancelled_exit_code(self, mock_watch, mock_find, mock_dispatch):
        """Returns EXIT_CANCELLED (2) when workflow conclusion is cancelled."""
        mock_find.return_value = WorkflowRun(
            run_id=444,
            url="https://github.com/google/xmc/actions/runs/444",
            head_sha="sha_target",
            event="workflow_dispatch",
            status="queued",
            created_at=datetime.now(timezone.utc),
        )
        mock_watch.return_value = RunOutcome(
            run_id=444,
            url="https://github.com/google/xmc/actions/runs/444",
            head_sha="sha_target",
            event="workflow_dispatch",
            conclusion="cancelled",
            superseded_by=555,
            failed_jobs=[],
            page_url=None,
        )

        parser = create_parser()
        args = parser.parse_args(
            [
                "--workflow",
                "nightly-build.yml",
                "--target-sha",
                "sha_target",
            ]
        )
        result = dispatch_and_watch(args)

        self.assertEqual(result["conclusion"], "cancelled")
        self.assertEqual(result["superseded_by"], 555)
        self.assertEqual(result["_exit_code"], EXIT_CANCELLED)

    @patch("tools.release.dispatch_and_watch.resolve_target_commit", return_value="expected_sha")
    @patch(
        "tools.release.dispatch_and_watch.dispatch_workflow",
        return_value=datetime.now(timezone.utc),
    )
    @patch("tools.release.dispatch_and_watch.find_dispatched_run")
    def test_dispatch_and_watch_sha_mismatch(self, mock_find, mock_dispatch, mock_resolve):
        """When SHA mismatch HandledError is raised, returns structured result with EXIT_SHA_MISMATCH."""
        mock_find.side_effect = HandledError(
            "Target SHA mismatch",
            "check git SHA",
            exit_code=EXIT_SHA_MISMATCH,
            details={
                "expected_sha": "expected_sha",
                "actual_sha": "actual_sha",
                "run_id": 999,
                "url": "https://github.com/google/xmc/actions/runs/999",
            },
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            state_file = Path(tmpdir) / "state.json"
            parser = create_parser()
            args = parser.parse_args(
                [
                    "--workflow",
                    "nightly-build.yml",
                    "--state-file",
                    str(state_file),
                ]
            )
            result = dispatch_and_watch(args)

            self.assertEqual(result["run_id"], 999)
            self.assertEqual(result["conclusion"], "sha_mismatch")
            self.assertEqual(result["head_sha"], "actual_sha")
            self.assertEqual(result["_exit_code"], EXIT_SHA_MISMATCH)

            with open(state_file, encoding="utf-8") as f:
                state_data = json.load(f)
            self.assertEqual(state_data["runs"]["nightly-build.yml"]["conclusion"], "sha_mismatch")

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_resolve_actor_success(self, mock_run, mock_which):
        """Resolves authenticated user from gh api user."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "api"], returncode=0, stdout="my-gh-user\n", stderr=""
        )
        actor = resolve_actor()
        self.assertEqual(actor, "my-gh-user")

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/usr/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_resolve_actor_failure_fallback(self, mock_run, mock_which):
        """Falls back to empty string if gh api user fails."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["gh", "api"], returncode=1, stdout="", stderr="error"
        )
        actor = resolve_actor()
        self.assertEqual(actor, "")

    @patch("tools.release.dispatch_and_watch.run_tool")
    def test_main(self, mock_run_tool):
        """Verifies main entrypoint passes parser and handler to run_tool."""
        main()
        mock_run_tool.assert_called_once()


if __name__ == "__main__":
    unittest.main()
