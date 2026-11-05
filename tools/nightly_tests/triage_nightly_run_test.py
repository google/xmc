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
from collections.abc import Callable
from pathlib import Path
from typing import Any
from unittest.mock import patch

from tools.common import tool_utils
from tools.common.tool_utils import HandledError
from tools.nightly_tests.triage_nightly_run import (
    _collect_recent_commits,
    _extract_error_excerpt,
    _load_json_object,
    _TriageContext,
    create_parser,
    triage_nightly_run,
)

_WORKFLOW_FIXTURE = """\
jobs:
  e2e:
    strategy:
      matrix:
        example:
          - id: simple_job
            name: "Simple Job"
          - id: resource_dashboard_ui
            name: "Resource Observability Dashboard (Playwright UI)"
"""


def _proc(
    stdout: str = "", returncode: int = 0, stderr: str = ""
) -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def _json_proc(payload: Any) -> subprocess.CompletedProcess[str]:
    return _proc(json.dumps(payload))


def _run_fixture(
    run_id: int = 9001,
    conclusion: str | None = "failure",
    sha: str = "9001aabbccdd",
    created_at: str = "2026-11-04T04:00:00Z",
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


def _job_fixture(
    job_id: int, name: str, conclusion: str, steps: list[tuple[str, str]] | None = None
) -> dict[str, Any]:
    return {
        "id": job_id,
        "name": name,
        "conclusion": conclusion,
        "steps": [{"name": step_name, "conclusion": concl} for step_name, concl in (steps or [])],
    }


def _artifacts_fixture(*active: str, expired: tuple[str, ...] = ()) -> dict[str, Any]:
    items = [{"name": name, "expired": False} for name in active]
    items.extend({"name": name, "expired": True} for name in expired)
    return {"artifacts": items}


_FAILING_RUN = _run_fixture()
_GREEN_BUILD = _run_fixture(8001, "success", "9001aabbccdd", "2026-11-04T01:00:00Z")
_TWO_FAILED_JOBS = [
    _job_fixture(11, "Run & Verify: Simple Job", "failure"),
    _job_fixture(22, "Run & Verify: Resource Observability Dashboard (Playwright UI)", "failure"),
]


class TriageNightlyRunTest(unittest.TestCase):
    """Hermetic unit tests for triage_nightly_run."""

    def setUp(self) -> None:
        super().setUp()
        self.tmpdir = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.repo_dir = self.tmpdir / "repo"
        workflow_file = self.repo_dir / ".github" / "workflows" / "nightly-examples-e2e.yml"
        workflow_file.parent.mkdir(parents=True)
        workflow_file.write_text(_WORKFLOW_FIXTURE, encoding="utf-8")
        self.mock_run = self.enterContext(
            patch("tools.nightly_tests.triage_nightly_run.run_command")
        )

    def _triage(self, *argv: str) -> dict[str, Any]:
        cli_args = list(argv)
        if not any(arg.startswith("--repo-dir") for arg in cli_args):
            cli_args.append(f"--repo-dir={self.repo_dir}")
        return triage_nightly_run(create_parser().parse_args(cli_args))

    def _mock_triage(
        self,
        e2e_runs: list[dict[str, Any]] | None = None,
        build_runs: list[dict[str, Any]] | None = None,
        *,
        issues: list[dict[str, Any]] | None = None,
        jobs: list[dict[str, Any]] | None = None,
        job_logs: list[subprocess.CompletedProcess[str]] | None = None,
        artifacts: dict[str, Any] | None = None,
        downloads: int = 0,
        recent_log: subprocess.CompletedProcess[str] | None = None,
        trailing: tuple[subprocess.CompletedProcess[str], ...] = (),
        on_download: Callable[[Path], Any] | None = None,
    ) -> None:
        seq = [
            _proc(),
            _json_proc([_FAILING_RUN] if e2e_runs is None else e2e_runs),
            _json_proc(issues or []),
            _json_proc({"jobs": jobs or []}),
            *(job_logs or []),
            _json_proc(artifacts or {"artifacts": []}),
            *([_proc() for _ in range(downloads)]),
            recent_log or _proc("abc1234 feat: recent change (#1540)\n"),
            _json_proc([_GREEN_BUILD] if build_runs is None else build_runs),
            *trailing,
        ]
        if on_download is None:
            self.mock_run.side_effect = seq
            return
        responses = iter(seq)

        def fake_run(cmd: list[str], **_kwargs: Any) -> subprocess.CompletedProcess[str]:
            res = next(responses)
            if cmd[:3] == ["gh", "run", "download"]:
                dest = Path(cmd[cmd.index("-D") + 1])
                dest.mkdir(parents=True, exist_ok=True)
                on_download(dest)
            return res

        self.mock_run.side_effect = fake_run

    def test_auto_discovers_failed_job_parses_result_and_error_excerpt(self) -> None:
        scratch = self.tmpdir / "triage_out"
        e2e_runs = [
            _run_fixture(9002, None, "9002abcdef12", "2026-11-04T05:00:00Z", status="in_progress"),
            _FAILING_RUN,
            _run_fixture(9000, "success", "900011223344", "2026-11-03T04:00:00Z"),
        ]
        jobs = [
            _job_fixture(11, "Run & Verify: Simple Job", "success"),
            _job_fixture(
                22,
                "Run & Verify: Resource Observability Dashboard (Playwright UI)",
                "failure",
                [("Launch Example", "success"), ("Verify Example Workload", "failure")],
            ),
        ]
        raw_log = (
            "2026-11-04T04:10:00.1Z \x1b[31mAssertionError: missing metric row\x1b[0m\n"
            "2026-11-04T04:10:01.2Z ##[error]Process completed with exit code 1.\n"
            "2026-11-04T04:10:05.0Z Uploading artifact result-resource_dashboard_ui\n"
        )
        result_json = {
            "id": "resource_dashboard_ui",
            "status": "VERIFY_FAILED",
            "launch_status": "success",
            "verify_status": "failure",
            "xid": "1042",
        }
        self._mock_triage(
            e2e_runs,
            issues=[{"number": 42, "title": "Failure", "url": "u", "createdAt": "t"}],
            jobs=jobs,
            job_logs=[_proc(raw_log)],
            artifacts=_artifacts_fixture("result-simple_job", "result-resource_dashboard_ui"),
            downloads=1,
            recent_log=_proc("9001aab fix(rod): change table selector (#1548)\n"),
            on_download=lambda dest: (dest / "resource_dashboard_ui.json").write_text(
                json.dumps(result_json), encoding="utf-8"
            ),
        )
        res = self._triage(f"--scratch-dir={scratch}")

        self.assertEqual(res["status_summary"], "failing")
        self.assertEqual(res["target_run"]["databaseId"], 9001)
        self.assertEqual(res["recent_commits"], ["9001aab fix(rod): change table selector (#1548)"])
        called_cmds = [call.args[0] for call in self.mock_run.call_args_list]
        self.assertIn(
            ["git", "log", "--oneline", "-n31", "900011223344..9001aabbccdd", "--"], called_cmds
        )
        failed_job = res["failed_jobs"][0]
        self.assertEqual(failed_job["example_id"], "resource_dashboard_ui")
        self.assertEqual(failed_job["failed_steps"], ["Verify Example Workload"])
        self.assertEqual(failed_job["result"], result_json)
        self.assertEqual(
            failed_job["error_excerpt"],
            ["AssertionError: missing metric row", "##[error]Process completed with exit code 1."],
        )
        self.assertEqual(res["warnings"], [])

    def test_download_artifacts_guards_symlinks_traversal_expiry_and_cleans_stale_paths(
        self,
    ) -> None:
        scratch = self.tmpdir / "triage_out"
        scratch.mkdir()
        (scratch / "ui-screenshots-symlink").symlink_to(self.tmpdir)
        stale_dir = scratch / "result-resource_dashboard_ui"
        stale_dir.mkdir()
        (stale_dir / "old.json").write_text("{}", encoding="utf-8")
        expired_dir = scratch / "result-simple_job"
        expired_dir.mkdir()
        (expired_dir / "simple_job.json").write_text('{"xid": "stale"}', encoding="utf-8")
        stale_file = scratch / "ui-screenshots-resource_dashboard_ui"
        stale_file.write_text("stale-file", encoding="utf-8")

        artifacts = _artifacts_fixture(
            "result-resource_dashboard_ui",
            "ui-screenshots-resource_dashboard_ui",
            "ui-screenshots-../traversal",
            "ui-screenshots-symlink",
            expired=("result-simple_job", "ui-screenshots-expired"),
        )
        self._mock_triage(
            jobs=_TWO_FAILED_JOBS,
            job_logs=[
                _proc("plain failure line\n" + "x" * 350 + "\n"),
                _proc(returncode=1, stderr="log 404"),
            ],
            artifacts=artifacts,
            downloads=2,
        )
        res = self._triage(f"--scratch-dir={scratch}")

        self.assertFalse((stale_dir / "old.json").exists())
        self.assertFalse(stale_file.is_file())
        self.assertIsNone(res["failed_jobs"][0]["result"])
        self.assertTrue(res["failed_jobs"][0]["error_excerpt"][-1].endswith("..."))
        self.assertEqual(res["failed_jobs"][1]["error_excerpt"], [])
        self.assertEqual(
            [Path(path).name for path in res["downloaded_artifacts"]],
            ["result-resource_dashboard_ui", "ui-screenshots-resource_dashboard_ui"],
        )

    def test_result_artifact_warns_on_symlink_or_corrupt_json(self) -> None:
        def populate_bad_results(dest: Path) -> None:
            if dest.name == "result-simple_job":
                (dest / "simple_job.json").symlink_to(self.tmpdir / "outside.json")
            else:
                (dest / "resource_dashboard_ui.json").write_text("[]", encoding="utf-8")

        self._mock_triage(
            jobs=_TWO_FAILED_JOBS,
            job_logs=[_proc("err1"), _proc("err2")],
            artifacts=_artifacts_fixture("result-simple_job", "result-resource_dashboard_ui"),
            downloads=2,
            on_download=populate_bad_results,
        )
        res = self._triage(f"--scratch-dir={self.tmpdir / 'corrupt_out'}")

        self.assertIsNone(res["failed_jobs"][0]["result"])
        self.assertIsNone(res["failed_jobs"][1]["result"])
        self.assertTrue(any("Skipped symlink result" in w for w in res["warnings"]))
        self.assertTrue(any("Expected JSON object" in w for w in res["warnings"]))

    def test_computes_rollout_skew_and_downloads_failed_build_diagnostics(self) -> None:
        build_runs = [
            _run_fixture(8003, "success", "8003eeff0011", "2026-11-04T06:00:00Z"),
            _run_fixture(8002, "failure", "8002deadbeef", "2026-11-04T01:00:00Z"),
            _run_fixture(8001, "success", "800111223344", "2026-11-03T01:00:00Z"),
        ]
        diff_files = "\n".join(
            (
                "cmd/rod_data_provider/main.go",
                "deploy/addons/metrics/setup_metrics_addons.sh",
                "deploy/clickhouse/schema.sql",
                "deploy/cli/install_xmanager.py",
                "README.md",
            )
        )
        self._mock_triage(
            build_runs=build_runs,
            trailing=(
                _proc("abc1234 feat(rod): add column\ndef5678 docs: readme\n"),
                _proc(diff_files),
                _json_proc(_artifacts_fixture("deploy-gke-diagnostics")),
                _proc(),
            ),
        )
        res = self._triage(f"--scratch-dir={self.tmpdir / 'skew_out'}")

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
        self.assertEqual(
            [Path(p).name for p in res["downloaded_artifacts"]], ["deploy-gke-diagnostics"]
        )

    def test_workflow_dispatch_green_run_does_not_supersede_scheduled_failure(self) -> None:
        dispatch_green = _run_fixture(
            9050, "success", "9050abcdef12", "2026-11-04T06:00:00Z", event="workflow_dispatch"
        )
        self._mock_triage([dispatch_green, _FAILING_RUN])
        res = self._triage(f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["status_summary"], "failing")
        self.assertEqual(res["target_run"]["databaseId"], 9001)
        self.assertIsNone(res["superseded_by_green_run"])
        self.assertIn("workflow_dispatch run 9050", res["warnings"][0])

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
        self.assertEqual(res["recent_commits"], [])
        self.assertEqual(res["warnings"], [])
        self.assertEqual(self.mock_run.call_count, 3)

    def test_auto_discovery_reports_no_completed_runs(self) -> None:
        in_progress = [_run_fixture(9300, None, "9300abcdef12", status="in_progress")]
        self.mock_run.side_effect = [_proc(), _json_proc(in_progress), _proc("[]")]
        res = self._triage()

        self.assertEqual(res["status_summary"], "no_completed_runs")
        self.assertTrue(any("No completed" in warning for warning in res["warnings"]))

    def test_explicit_run_id_superseded_and_symlink_log_guard(self) -> None:
        (self.tmpdir / "job_77.log").symlink_to(self.tmpdir / "outside.txt")
        e2e_runs = [_run_fixture(9200, "success", "9200aabbccdd", "2026-11-04T04:00:00Z")]
        explicit_run = _run_fixture(9150, "cancelled", "915011223344", "2026-11-01T04:00:00Z")
        newer_builds = [_run_fixture(8500, "failure", "8500deadbeef", "2026-11-04T01:00:00Z")]
        self.mock_run.side_effect = [
            _proc(returncode=1, stderr="offline"),
            _json_proc(e2e_runs),
            _proc("[]"),
            _json_proc(explicit_run),
            _json_proc({"jobs": [_job_fixture(77, "Build Images", "cancelled")]}),
            _proc("##[error]The operation was canceled.\n"),
            _json_proc({"artifacts": []}),
            _proc("9150112 chore: cancelled commit (#1501)\n"),
            _json_proc(newer_builds),
        ]
        res = self._triage("--run-id=9150", f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["status_summary"], "superseded")
        self.assertEqual(res["superseded_by_green_run"]["databaseId"], 9200)
        self.assertIsNone(res["failed_jobs"][0]["log_path"])
        self.assertEqual(
            res["failed_jobs"][0]["error_excerpt"], ["##[error]The operation was canceled."]
        )
        self.assertIsNone(res["rollout_skew"]["active_build_run"])
        for expected in ("git fetch", "Skipped symlink", "15-run lookback", "No prior green"):
            self.assertTrue(any(expected in warning for warning in res["warnings"]), expected)

    def test_explicit_run_id_returns_early_when_in_progress_or_healthy(self) -> None:
        green_run = _run_fixture(9200, "success", "9200aabbccdd", "2026-11-04T04:00:00Z")
        in_progress = _run_fixture(
            9100, None, "9100abcdef12", "2026-11-01T04:00:00Z", status="in_progress"
        )
        for pinned_run, expected_status in ((in_progress, "in_progress"), (green_run, "healthy")):
            with self.subTest(expected_status=expected_status):
                self.mock_run.reset_mock()
                self.mock_run.side_effect = [
                    _proc(),
                    _json_proc([green_run]),
                    _proc("[]"),
                    _json_proc(pinned_run),
                ]
                res = self._triage(f"--run-id={pinned_run['databaseId']}")
                self.assertEqual(res["status_summary"], expected_status)
                self.assertIsNone(res["superseded_by_green_run"])
                self.assertIsNone(res["scratch_dir"])
                self.assertEqual(self.mock_run.call_count, 4)

    def test_rollout_skew_and_recent_commits_git_failures_record_warnings(self) -> None:
        self._mock_triage(
            build_runs=[_run_fixture(8001, "success", "800111223344", "2026-11-04T01:00:00Z")],
            recent_log=_proc(returncode=128, stderr="missing object"),
            trailing=(_proc(returncode=128, stderr="fatal: bad revision"), _proc("")),
        )
        res = self._triage(f"--scratch-dir={self.tmpdir}")

        self.assertEqual(res["recent_commits"], [])
        self.assertEqual(res["rollout_skew"]["undeployed_commits"], [])
        self.assertTrue(any("Recent commits git log" in warning for warning in res["warnings"]))
        self.assertTrue(any("git inspect" in warning for warning in res["warnings"]))

    def test_invalid_shas_warn_and_uses_default_scratch_dir(self) -> None:
        self.mock_run.side_effect = [
            _proc(),
            _json_proc([_run_fixture(sha="--bad-sha")]),
            _proc("[]"),
            _json_proc({"jobs": []}),
            _json_proc({"artifacts": []}),
            _json_proc([_run_fixture(8001, "success", "bad..sha", "2026-11-04T01:00:00Z")]),
        ]
        with patch.dict("os.environ", {"ARTIFACT_DIR": ""}), patch.object(Path, "mkdir"):
            res = self._triage()

        self.assertEqual(res["scratch_dir"], "/tmp/nightly_triage_9001")
        self.assertEqual(res["recent_commits"], [])
        self.assertTrue(any("target E2E run" in warning for warning in res["warnings"]))
        self.assertTrue(
            any("Invalid commit SHA on build" in warning for warning in res["warnings"])
        )

    def test_cli_errors_and_missing_binary_handling(self) -> None:
        with self.assertRaises(HandledError):
            self._triage("--run-id=0")
        symlink_scratch = self.tmpdir / "symlink_scratch"
        symlink_scratch.symlink_to(self.tmpdir)
        cases: tuple[tuple[list[str], list[subprocess.CompletedProcess[str]], str], ...] = (
            (
                [f"--scratch-dir={symlink_scratch}"],
                [_proc(), _json_proc([_FAILING_RUN]), _proc("[]")],
                "symlink scratch",
            ),
            ([], [_proc(), _proc(returncode=1, stderr="HTTP 401")], "HTTP 401"),
            ([], [_proc(), _proc("")], "Invalid JSON"),
        )
        for cli_args, mocks, pattern in cases:
            with self.subTest(pattern=pattern), self.assertRaisesRegex(HandledError, pattern):
                self.mock_run.side_effect = mocks
                self._triage(*cli_args)

        with (
            patch(
                "tools.nightly_tests.triage_nightly_run.run_command", wraps=tool_utils.run_command
            ),
            patch("tools.common.tool_utils.subprocess.run", side_effect=FileNotFoundError("git")),
            self.assertRaisesRegex(HandledError, "Cannot run git"),
        ):
            self._triage()

    def test_extract_error_excerpt_ignores_echoed_commands_and_keeps_repeated_errors(self) -> None:
        echoed_only = (
            "2026-11-04T04:01:00Z ##[group]Run Launch Example Workload\n"
            '2026-11-04T04:01:00Z \x1b[36;1m  echo "::error::Failed to parse XID."\x1b[0m\n'
            "2026-11-04T04:01:05Z tail line after group\n"
        )
        self.assertEqual(_extract_error_excerpt(echoed_only)[-1], "tail line after group")

        repeated_log = "\n".join(
            [
                "\ufeff##[error]Process completed with exit code 1.",
                *[f"2026-11-04T04:02:{i:02d}Z step line {i}" for i in range(20)],
                "2026-11-04T04:03:00Z ##[error]Process completed with exit code 1.",
            ]
        )
        excerpt = _extract_error_excerpt(repeated_log)
        self.assertEqual(excerpt[-1], "##[error]Process completed with exit code 1.")
        self.assertIn("step line 19", excerpt)

    def test_collect_recent_commits_warns_on_equal_sha_truncation_and_fallbacks(self) -> None:
        prior = _run_fixture(9000, "success", "AABBCCDDEEFF", "2026-11-03T04:00:00Z")
        same_sha_ctx = _TriageContext(repo_dir=self.repo_dir, e2e_runs=[prior])
        self.assertEqual(
            _collect_recent_commits(same_sha_ctx, _run_fixture(sha="aabbccddeeff")), []
        )
        self.assertIn("Layer B cluster drift", same_sha_ctx.warnings[0])

        new_target = _run_fixture(9002, "failure", "112233445566")
        trunc_ctx = _TriageContext(repo_dir=self.repo_dir, e2e_runs=[prior])
        self.mock_run.side_effect = [_proc("\n".join(f"c{i:02d} msg" for i in range(31)))]
        self.assertEqual(len(_collect_recent_commits(trunc_ctx, new_target)), 30)
        self.assertIn("truncated to newest 30", trunc_ctx.warnings[0])

        fallback_ctx = _TriageContext(repo_dir=self.repo_dir, e2e_runs=[prior])
        self.mock_run.side_effect = [_proc(""), _proc("1122334 fallback commit\n")]
        self.assertEqual(
            _collect_recent_commits(fallback_ctx, new_target), ["1122334 fallback commit"]
        )
        self.assertIn("empty range", fallback_ctx.warnings[0])

    def test_load_json_object_handles_missing_corrupt_oversized_and_directory_paths(self) -> None:
        ctx = _TriageContext(repo_dir=self.repo_dir)
        self.assertIsNone(_load_json_object(ctx, self.tmpdir / "missing.json"))
        self.assertEqual(ctx.warnings, [])

        (self.tmpdir / "bad.json").write_text("{broken", encoding="utf-8")
        (self.tmpdir / "huge.json").write_bytes(b"{" + b" " * 70000 + b"}")
        (self.tmpdir / "dir.json").mkdir()
        for name in ("bad.json", "huge.json", "dir.json"):
            self.assertIsNone(_load_json_object(ctx, self.tmpdir / name))
        self.assertEqual(len(ctx.warnings), 3)


if __name__ == "__main__":
    unittest.main()
